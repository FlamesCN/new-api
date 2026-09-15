package helper

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const invalidEncryptedContentCode = "invalid_encrypted_content"

func IsInvalidEncryptedContentError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if strings.EqualFold(string(err.GetErrorCode()), invalidEncryptedContentCode) {
		return true
	}
	oai := err.ToOpenAIError()
	code := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", oai.Code)))
	if code == invalidEncryptedContentCode {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(oai.Message + " " + err.Error()))
	return strings.Contains(msg, invalidEncryptedContentCode) ||
		strings.Contains(msg, "encrypted content could not be decrypted")
}

func StripEncryptedReasoningFromResponsesRequest(req *dto.OpenAIResponsesRequest) (int, error) {
	if req == nil || len(req.Input) == 0 {
		return 0, nil
	}
	stripped, n, err := StripEncryptedReasoningItems(req.Input)
	if err != nil {
		return 0, err
	}
	req.Input = stripped
	return n, nil
}

// StripEncryptedReasoningFromResponsesBody preserves fields not represented by
// the request DTO, including provider extensions and their numeric tokens.
func StripEncryptedReasoningFromResponsesBody(body []byte) ([]byte, int, error) {
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body, &fields); err != nil {
		return body, 0, err
	}
	input, n, err := StripEncryptedReasoningItems(fields["input"])
	if err != nil || n == 0 {
		return body, 0, err
	}
	fields["input"] = input
	out, err := common.Marshal(fields)
	if err != nil {
		return body, 0, err
	}
	return out, n, nil
}

func StripEncryptedReasoningItems(input json.RawMessage) (json.RawMessage, int, error) {
	if len(input) == 0 {
		return input, 0, nil
	}
	n := 0
	var stripped any
	switch common.GetJsonType(input) {
	case "array":
		var items []json.RawMessage
		if err := common.Unmarshal(input, &items); err != nil {
			return input, 0, err
		}
		out := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			if common.GetJsonType(item) == "object" {
				var fields map[string]json.RawMessage
				if err := common.Unmarshal(item, &fields); err != nil {
					return input, 0, err
				}
				if isEncryptedReasoningItem(fields) {
					n++
					continue
				}
			}
			child, count, err := StripEncryptedReasoningItems(item)
			if err != nil {
				return input, 0, err
			}
			n += count
			out = append(out, child)
		}
		stripped = out
	case "object":
		var fields map[string]json.RawMessage
		if err := common.Unmarshal(input, &fields); err != nil {
			return input, 0, err
		}
		for key, value := range fields {
			child, count, err := StripEncryptedReasoningItems(value)
			if err != nil {
				return input, 0, err
			}
			if count > 0 {
				fields[key] = child
				n += count
			}
		}
		stripped = fields
	default:
		var raw json.RawMessage
		if err := common.Unmarshal(input, &raw); err != nil {
			return input, 0, err
		}
	}
	if n == 0 {
		return input, 0, nil
	}
	out, err := common.Marshal(stripped)
	if err != nil {
		return input, 0, err
	}
	return out, n, nil
}

func isEncryptedReasoningItem(fields map[string]json.RawMessage) bool {
	var itemType string
	if err := common.Unmarshal(fields["type"], &itemType); err != nil || itemType != "reasoning" {
		return false
	}
	raw := fields["encrypted_content"]
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	if common.GetJsonType(raw) == "string" {
		var value string
		if err := common.Unmarshal(raw, &value); err != nil {
			return false
		}
		return strings.TrimSpace(value) != ""
	}
	return true
}
