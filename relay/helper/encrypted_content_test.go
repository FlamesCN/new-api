package helper

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsInvalidEncryptedContentError(t *testing.T) {
	require.False(t, IsInvalidEncryptedContentError(nil))
	require.False(t, IsInvalidEncryptedContentError(types.NewErrorWithStatusCode(
		assert.AnError, types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest,
	)))

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "The encrypted content for item rs_abc could not be verified. Reason: Encrypted content could not be decrypted or parsed.",
		Type:    "invalid_request_error",
		Code:    "invalid_encrypted_content",
	}, http.StatusBadRequest)
	require.True(t, IsInvalidEncryptedContentError(err))
}

func TestStripEncryptedReasoningItems_DropsReasoningKeepsVisibleTurns(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"reasoning","id":"rs_1","encrypted_content":"gAAA-blob","summary":[{"type":"summary_text","text":"plan"}]},
		{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"}
	]`)
	got, n, err := StripEncryptedReasoningItems(input)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var items []map[string]any
	require.NoError(t, common.Unmarshal(got, &items))
	require.Len(t, items, 2)
	assert.Equal(t, "message", items[0]["type"])
	assert.Equal(t, "function_call", items[1]["type"])
}

func TestStripEncryptedReasoningItems_NestedReplacementHistory(t *testing.T) {
	input := json.RawMessage(`[
		{
			"type":"compacted",
			"payload":{
				"replacement_history":[
					{"type":"message","role":"user","content":"keep"},
					{"type":"reasoning","id":"rs_nested","encrypted_content":"blob"}
				]
			}
		}
	]`)
	got, n, err := StripEncryptedReasoningItems(input)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var items []map[string]any
	require.NoError(t, common.Unmarshal(got, &items))
	require.Len(t, items, 1)
	payload, ok := items[0]["payload"].(map[string]any)
	require.True(t, ok)
	history, ok := payload["replacement_history"].([]any)
	require.True(t, ok)
	require.Len(t, history, 1)
	kept, ok := history[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "message", kept["type"])
}

func TestStripEncryptedReasoningItems_PreservesNumericTokens(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"reasoning","encrypted_content":"blob"},
		{"type":"message","role":"user","content":"keep","provider_metadata":{"record_id":9007199254740993,"decimal":0.1234567890123456789,"exponent":1e400}},
		{"type":"function_call","call_id":"c1","name":"shell","arguments":"{\"id\":9007199254740993}"}
	]`)
	got, n, err := StripEncryptedReasoningItems(input)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var items []json.RawMessage
	require.NoError(t, common.Unmarshal(got, &items))
	require.Len(t, items, 2)
	assert.Contains(t, string(items[0]), `"record_id":9007199254740993`)
	assert.Contains(t, string(items[0]), `"decimal":0.1234567890123456789`)
	assert.Contains(t, string(items[0]), `"exponent":1e400`)
	assert.Contains(t, string(items[1]), `"arguments":"{\"id\":9007199254740993}"`)
}

func TestStripEncryptedReasoningItems_PreservesNonEncryptedItems(t *testing.T) {
	for _, input := range []string{
		`"plain input"`, `null`,
		`[{"type":"reasoning","summary":[]},{"type":"reasoning","encrypted_content":null},{"type":"reasoning","encrypted_content":"  "},{"type":"compaction","encrypted_content":"keep"}]`,
	} {
		t.Run(input, func(t *testing.T) {
			got, n, err := StripEncryptedReasoningItems(json.RawMessage(input))
			require.NoError(t, err)
			assert.Zero(t, n)
			assert.Equal(t, input, string(got))
		})
	}
}

func TestStripEncryptedReasoningFromResponsesBody_OnlyChangesInput(t *testing.T) {
	body := []byte(`{"input":[{"type":"reasoning","encrypted_content":"drop"},{"role":"user","content":"keep"}],"provider_extension":[{"type":"reasoning","encrypted_content":"keep","id":9007199254740993}],"background":true,"number":1e400}`)
	got, n, err := StripEncryptedReasoningFromResponsesBody(body)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var fields map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(got, &fields))
	assert.Equal(t, `[{"role":"user","content":"keep"}]`, string(fields["input"]))
	assert.Equal(t, `[{"type":"reasoning","encrypted_content":"keep","id":9007199254740993}]`, string(fields["provider_extension"]))
	assert.Equal(t, `true`, string(fields["background"]))
	assert.Equal(t, `1e400`, string(fields["number"]))
}

func TestStripEncryptedReasoningFromResponsesBody_NoChangeOnInvalidOrUnencryptedInput(t *testing.T) {
	for _, tt := range []struct {
		body    string
		invalid bool
	}{
		{body: `{"input": [`, invalid: true},
		{body: `{"model": "gpt-test", "input": "hello"}`},
		{body: `{"model": "gpt-test"}`},
	} {
		t.Run(tt.body, func(t *testing.T) {
			got, n, err := StripEncryptedReasoningFromResponsesBody([]byte(tt.body))
			if tt.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Zero(t, n)
			assert.Equal(t, tt.body, string(got))
		})
	}
}

func TestStripEncryptedReasoningFromResponsesRequest_NoopWithoutBlobs(t *testing.T) {
	req := &dto.OpenAIResponsesRequest{
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hi"}]`),
	}
	original := string(req.Input)
	n, err := StripEncryptedReasoningFromResponsesRequest(req)
	require.NoError(t, err)
	require.Equal(t, 0, n)
	assert.Equal(t, original, string(req.Input))
}
