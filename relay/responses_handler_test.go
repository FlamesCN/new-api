package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesHelper_EncryptedRetryPreservesOutboundRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name                                        string
		channelPassthrough, globalPassthrough, disk bool
	}{
		{name: "channel_passthrough", channelPassthrough: true},
		{name: "global_passthrough", globalPassthrough: true},
		{name: "converted"},
		{name: "disk_passthrough", channelPassthrough: true, disk: true},
		{name: "disk_converted", disk: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			passthrough := tt.channelPassthrough || tt.globalPassthrough
			global := model_setting.GetGlobalSettings()
			originalPassThrough := global.PassThroughRequestEnabled
			global.PassThroughRequestEnabled = tt.globalPassthrough
			t.Cleanup(func() { global.PassThroughRequestEnabled = originalPassThrough })
			originalDiskConfig := common.GetDiskCacheConfig()
			common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: tt.disk, ThresholdMB: 0, MaxSizeMB: 64, Path: t.TempDir()})
			t.Cleanup(func() { common.SetDiskCacheConfig(originalDiskConfig) })

			const payload = `{"model":"gpt-test","background":true,"provider_extension":{"id":9007199254740993},"input":[{"type":"reasoning","encrypted_content":"blob"},{"role":"user","content":"hello"},{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"done"}]}`
			type capturedRequest struct {
				body []byte
				err  error
			}
			requests := make(chan capturedRequest, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				requests <- capturedRequest{body, err}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"Encrypted content could not be decrypted","type":"invalid_request_error","code":"invalid_encrypted_content"}}`)
			}))
			defer server.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
			ctx.Request.Header.Set("Content-Type", "application/json")
			defer common.CleanupBodyStorage(ctx)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-test")
			common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: tt.channelPassthrough})
			ctx.Set("model_mapping", `{"gpt-test":"gpt-mapped"}`)
			overrides := map[string]interface{}{
				"background":         false,
				"provider_extension": map[string]interface{}{"from_override": true},
			}
			var request dto.OpenAIResponsesRequest
			require.NoError(t, common.Unmarshal([]byte(payload), &request))
			if !passthrough {
				// Reapplying this override would put encrypted reasoning back.
				overrides["input"] = request.Input
			}
			common.SetContextKey(ctx, constant.ContextKeyChannelParamOverride, overrides)
			info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "gpt-test", RequestURLPath: "/v1/responses", RelayMode: relayconstant.RelayModeResponses}
			err := ResponsesHelper(ctx, info)
			require.NotNil(t, err)
			assert.Equal(t, "invalid_encrypted_content", string(err.GetErrorCode()))
			assert.Contains(t, string(request.Input), `"encrypted_content":"blob"`, "a retry must not mutate the original request")
			require.Len(t, requests, 2, "retry must be bounded to one extra attempt")
			first, second := <-requests, <-requests
			require.NoError(t, first.err)
			require.NoError(t, second.err)
			var before, after map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(first.body, &before))
			require.NoError(t, common.Unmarshal(second.body, &after))
			var input []json.RawMessage
			require.NoError(t, common.Unmarshal(after["input"], &input))
			require.Len(t, input, 3)
			assert.JSONEq(t, `{"role":"user","content":"hello"}`, string(input[0]))
			assert.JSONEq(t, `{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"}`, string(input[1]))
			assert.JSONEq(t, `{"type":"function_call_output","call_id":"c1","output":"done"}`, string(input[2]))
			delete(before, "input")
			delete(after, "input")
			assert.Equal(t, before, after, "only encrypted reasoning may change")
			if passthrough {
				assert.Equal(t, payload, string(first.body))
				assert.Equal(t, `true`, string(after["background"]))
				assert.Equal(t, `{"id":9007199254740993}`, string(after["provider_extension"]))
				assert.Equal(t, `"gpt-test"`, string(after["model"]))
			} else {
				assert.Equal(t, `"gpt-mapped"`, string(after["model"]))
				assert.Equal(t, `false`, string(after["background"]))
			}
		})
	}
}

func TestResponsesHelper_DoesNotRetryWithoutMatchingErrorAndEncryptedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, input, code string
	}{
		{"different_error", `[{"type":"reasoning","encrypted_content":"blob"}]`, "invalid_request"},
		{"no_encrypted_input", `[{"role":"user","content":"hello"}]`, "invalid_encrypted_content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				attempts.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"rejected","type":"invalid_request_error","code":"`+tt.code+`"}}`)
			}))
			defer server.Close()
			request := dto.OpenAIResponsesRequest{Model: "gpt-test", Input: json.RawMessage(tt.input)}
			payload, marshalErr := common.Marshal(request)
			require.NoError(t, marshalErr)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(payload)))
			ctx.Request.Header.Set("Content-Type", "application/json")
			defer common.CleanupBodyStorage(ctx)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-test")
			common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
			info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "gpt-test", RequestURLPath: "/v1/responses", RelayMode: relayconstant.RelayModeResponses}
			err := ResponsesHelper(ctx, info)
			require.NotNil(t, err)
			assert.Equal(t, tt.code, string(err.GetErrorCode()))
			assert.EqualValues(t, 1, attempts.Load())
		})
	}
}

type responsesReplayTrackingStorage struct {
	common.BodyStorage
	replayReads atomic.Int32
}

func (s *responsesReplayTrackingStorage) NewReader() (io.ReadCloser, error) {
	s.replayReads.Add(1)
	return s.BodyStorage.NewReader()
}

func TestResponsesHelper_EncryptedRetryBodySizeLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name         string
		disk         bool
		size         int
		wantAttempts int32
	}{
		{name: "memory_at_limit", size: 1 << 20, wantAttempts: 2},
		{name: "memory_over_limit", size: (1 << 20) + 1, wantAttempts: 1},
		{name: "disk_at_limit", disk: true, size: 1 << 20, wantAttempts: 2},
		{name: "disk_over_limit", disk: true, size: (1 << 20) + 1, wantAttempts: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			originalDiskConfig := common.GetDiskCacheConfig()
			common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: tt.disk, ThresholdMB: 0, MaxSizeMB: 64, Path: t.TempDir()})
			t.Cleanup(func() { common.SetDiskCacheConfig(originalDiskConfig) })

			const prefix = `{"model":"gpt-test","input":[{"type":"reasoning","encrypted_content":"stale"},{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,`
			const suffix = `"}]}]}`
			payload := []byte(prefix + strings.Repeat("A", tt.size-len(prefix)-len(suffix)) + suffix)
			storage, err := common.CreateBodyStorage(payload)
			require.NoError(t, err)
			trackedStorage := &responsesReplayTrackingStorage{BodyStorage: storage}
			defer trackedStorage.Close()
			require.Equal(t, tt.disk, storage.IsDisk())

			var attempts atomic.Int32
			var firstSize atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n, _ := io.Copy(io.Discard, r.Body)
				if attempts.Add(1) == 1 {
					firstSize.Store(n)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"Encrypted content could not be decrypted","type":"invalid_request_error","code":"invalid_encrypted_content"}}`)
			}))
			defer server.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set(common.KeyBodyStorage, trackedStorage)
			ctx.Set("status_code_mapping", `{"400":"422"}`)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-test")
			common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
			var request dto.OpenAIResponsesRequest
			require.NoError(t, common.Unmarshal(payload, &request))
			info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "gpt-test", RequestURLPath: "/v1/responses", RelayMode: relayconstant.RelayModeResponses}
			relayErr := ResponsesHelper(ctx, info)
			require.NotNil(t, relayErr)
			assert.Equal(t, "invalid_encrypted_content", string(relayErr.GetErrorCode()))
			assert.Equal(t, http.StatusUnprocessableEntity, relayErr.StatusCode)
			assert.EqualValues(t, tt.size, firstSize.Load(), "the initial request must not be rejected or truncated")
			assert.Equal(t, tt.wantAttempts, attempts.Load())
			assert.Equal(t, tt.wantAttempts-1, trackedStorage.replayReads.Load(), "oversized bodies must not be opened for recovery")
		})
	}
}
