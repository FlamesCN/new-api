package relay

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// Recovery parses and re-encodes JSON in memory even for disk-backed bodies.
// Keep this optional retry bounded independently of the inbound request limit.
const maxEncryptedContentRetryBodyBytes int64 = 1 << 20

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	var responsesReq *dto.OpenAIResponsesRequest
	switch req := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		responsesReq = req
	case *dto.OpenAIResponsesCompactionRequest:
		// Only fields documented for POST /v1/responses/compact are forwarded:
		// model, input, instructions, previous_response_id, prompt_cache_key,
		// prompt_cache_options, prompt_cache_retention, service_tier.
		// Undocumented Codex-parity fields (tools, reasoning, text) are parsed
		// for client compatibility but intentionally not sent upstream.
		responsesReq = &dto.OpenAIResponsesRequest{
			Model:                req.Model,
			Input:                req.Input,
			Instructions:         req.Instructions,
			PreviousResponseID:   req.PreviousResponseID,
			ParallelToolCalls:    req.ParallelToolCalls,
			ServiceTier:          req.ServiceTier,
			PromptCacheKey:       req.PromptCacheKey,
			PromptCacheOptions:   req.PromptCacheOptions,
			PromptCacheRetention: req.PromptCacheRetention,
		}
	default:
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest or dto.OpenAIResponsesCompactionRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	adaptor, preparedBody, closer, apiErr := PrepareResponsesRequest(c, info, responsesReq)
	if apiErr != nil {
		return apiErr
	}
	defer closer.Close()

	statusCodeMappingStr := c.GetString("status_code_mapping")
	strippedEncryptedReasoning := false
	var retryBody []byte
	var httpResp *http.Response
	for {
		var requestBody common.ReplayableBody
		if strippedEncryptedReasoning {
			body, closer, err := relaycommon.NewOutboundJSONBody(retryBody)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			defer closer.Close()
			requestBody = body
			retryBody = nil
		} else {
			requestBody = preparedBody
		}

		resp, err := adaptor.DoRequest(c, info, requestBody)
		if err != nil {
			return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
		}

		if resp == nil {
			break
		}
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode == http.StatusOK {
			break
		}

		newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		if !strippedEncryptedReasoning && helper.IsInvalidEncryptedContentError(newAPIError) {
			if size := requestBody.Size(); size <= 0 || size > maxEncryptedContentRetryBodyBytes {
				logger.LogWarn(c, fmt.Sprintf("skipping encrypted_content recovery: body size %d exceeds recovery bounds (max %d bytes)", size, maxEncryptedContentRetryBodyBytes))
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
			// Retry the actual outbound body without reapplying conversion or
			// overrides; passthrough requests must retain their unknown fields.
			reader, readErr := requestBody.NewReader()
			if readErr != nil {
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
			outboundBody, readErr := io.ReadAll(io.LimitReader(reader, maxEncryptedContentRetryBodyBytes+1))
			_ = reader.Close()
			if readErr != nil || int64(len(outboundBody)) > maxEncryptedContentRetryBodyBytes {
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
			strippedBody, removed, stripErr := helper.StripEncryptedReasoningFromResponsesBody(outboundBody)
			if stripErr == nil && removed > 0 {
				strippedEncryptedReasoning = true
				retryBody = strippedBody
				logger.LogWarn(c, fmt.Sprintf("upstream rejected encrypted_content, stripped %d reasoning item(s) and retrying once", removed))
				continue
			}
		}
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	usageDto := usage.(*dto.Usage)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact {
		originModelName := info.OriginModelName
		originPriceData := info.PriceData

		_, err := helper.ModelPriceHelper(c, info, info.GetEstimatePromptTokens(), &types.TokenCountMeta{})
		if err != nil {
			info.OriginModelName = originModelName
			info.PriceData = originPriceData
			return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithSkipRetry(), types.ErrOptionWithStatusCode(http.StatusBadRequest))
		}
		service.PostTextConsumeQuota(c, info, usageDto, nil)

		info.OriginModelName = originModelName
		info.PriceData = originPriceData
		return nil
	}

	ConsumeResponsesQuota(c, info, usageDto)
	return nil
}

// ConsumeResponsesQuota applies the same settlement dispatch to HTTP and
// WebSocket Responses usage. Compact requests keep their separate repricing.
func ConsumeResponsesQuota(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) {
	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usage, "")
		return
	}
	service.PostTextConsumeQuota(c, info, usage, nil)
}
