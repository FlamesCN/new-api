package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	relaykittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildChannelAffinityTemplateContextForTest(meta channelAffinityMeta) *gin.Context {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	setChannelAffinityContext(ctx, meta)
	return ctx
}

func TestApplyChannelAffinityOverrideTemplate_NoTemplate(t *testing.T) {
	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName: "rule-no-template",
	})
	base := map[string]any{
		"temperature": 0.7,
	}

	merged, applied := ApplyChannelAffinityOverrideTemplate(ctx, base)
	require.False(t, applied)
	require.Equal(t, base, merged)
}

func TestApplyChannelAffinityOverrideTemplate_MergeTemplate(t *testing.T) {
	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName: "rule-with-template",
		ParamTemplate: map[string]any{
			"temperature": 0.2,
			"top_p":       0.95,
		},
		UsingGroup:     "default",
		ModelName:      "gpt-4.1",
		RequestPath:    "/v1/responses",
		KeySourceType:  "gjson",
		KeySourcePath:  "prompt_cache_key",
		KeyHint:        "abcd...wxyz",
		KeyFingerprint: "abcd1234",
	})
	base := map[string]any{
		"temperature": 0.7,
		"max_tokens":  2000,
	}

	merged, applied := ApplyChannelAffinityOverrideTemplate(ctx, base)
	require.True(t, applied)
	require.Equal(t, 0.7, merged["temperature"])
	require.Equal(t, 0.95, merged["top_p"])
	require.Equal(t, 2000, merged["max_tokens"])
	require.Equal(t, 0.7, base["temperature"])

	anyInfo, ok := ctx.Get(ginKeyChannelAffinityLogInfo)
	require.True(t, ok)
	info, ok := anyInfo.(map[string]any)
	require.True(t, ok)
	overrideInfoAny, ok := info["override_template"]
	require.True(t, ok)
	overrideInfo, ok := overrideInfoAny.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, overrideInfo["applied"])
	require.Equal(t, "rule-with-template", overrideInfo["rule_name"])
	require.EqualValues(t, 2, overrideInfo["param_override_keys"])
}

func TestApplyChannelAffinityOverrideTemplate_MergeOperations(t *testing.T) {
	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName: "rule-with-ops-template",
		ParamTemplate: map[string]any{
			"operations": []map[string]any{
				{
					"mode":  "pass_headers",
					"value": []string{"Originator"},
				},
			},
		},
	})
	base := map[string]any{
		"temperature": 0.7,
		"operations": []map[string]any{
			{
				"path":  "model",
				"mode":  "trim_prefix",
				"value": "openai/",
			},
		},
	}

	merged, applied := ApplyChannelAffinityOverrideTemplate(ctx, base)
	require.True(t, applied)
	require.Equal(t, 0.7, merged["temperature"])

	opsAny, ok := merged["operations"]
	require.True(t, ok)
	ops, ok := opsAny.([]any)
	require.True(t, ok)
	require.Len(t, ops, 2)

	firstOp, ok := ops[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pass_headers", firstOp["mode"])

	secondOp, ok := ops[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "trim_prefix", secondOp["mode"])
}

func TestShouldSkipRetryAfterChannelAffinityFailure(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() *gin.Context
		want bool
	}{
		{
			name: "nil context",
			ctx: func() *gin.Context {
				return nil
			},
			want: false,
		},
		{
			name: "explicit skip retry flag in context",
			ctx: func() *gin.Context {
				ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
					RuleName:   "rule-explicit-flag",
					SkipRetry:  false,
					UsingGroup: "default",
					ModelName:  "gpt-5",
				})
				ctx.Set(ginKeyChannelAffinitySkipRetry, true)
				return ctx
			},
			want: true,
		},
		{
			name: "fallback to matched rule meta",
			ctx: func() *gin.Context {
				return buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
					RuleName:   "rule-skip-retry",
					SkipRetry:  true,
					UsingGroup: "default",
					ModelName:  "gpt-5",
				})
			},
			want: true,
		},
		{
			name: "no flag and no skip retry meta",
			ctx: func() *gin.Context {
				return buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
					RuleName:   "rule-no-skip-retry",
					SkipRetry:  false,
					UsingGroup: "default",
					ModelName:  "gpt-5",
				})
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ShouldSkipRetryAfterChannelAffinityFailure(tt.ctx()))
		})
	}
}

func TestShouldSkipRetryAfterChannelAffinityDisabledChannel(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	originalRetryOnDisabledChannel := setting.RetryOnDisabledChannel
	setting.RetryOnDisabledChannel = true
	t.Cleanup(func() {
		setting.RetryOnDisabledChannel = originalRetryOnDisabledChannel
	})

	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName:   "rule-disabled-channel",
		SkipRetry:  true,
		UsingGroup: "default",
		ModelName:  "gpt-5",
	})
	require.False(t, ShouldSkipRetryAfterChannelAffinityDisabledChannel(ctx))

	setting.RetryOnDisabledChannel = false
	require.True(t, ShouldSkipRetryAfterChannelAffinityDisabledChannel(ctx))
}

func TestShouldSkipRetryAfterChannelAffinityError_DisabledChannel(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	originalRetryOnDisabledChannel := setting.RetryOnDisabledChannel
	setting.RetryOnDisabledChannel = true
	t.Cleanup(func() {
		setting.RetryOnDisabledChannel = originalRetryOnDisabledChannel
	})

	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName:   "rule-disabled-channel-error",
		SkipRetry:  true,
		UsingGroup: "default",
		ModelName:  "gpt-5",
	})
	disabledErr := relaykittypes.WithOpenAIError(relaykittypes.OpenAIError{
		Message: "该渠道已被禁用",
		Type:    "forbidden",
		Code:    "channel_disabled",
	}, http.StatusForbidden)

	require.False(t, ShouldSkipRetryAfterChannelAffinityError(ctx, disabledErr))

	setting.RetryOnDisabledChannel = false
	require.True(t, ShouldSkipRetryAfterChannelAffinityError(ctx, disabledErr))
}

func TestShouldSkipRetryAfterChannelAffinityError_QuotaExceeded(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	originalRetryOnChannelQuotaExceeded := setting.RetryOnChannelQuotaExceeded
	setting.RetryOnChannelQuotaExceeded = true
	t.Cleanup(func() {
		setting.RetryOnChannelQuotaExceeded = originalRetryOnChannelQuotaExceeded
	})

	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName:   "rule-quota-exceeded",
		SkipRetry:  true,
		UsingGroup: "default",
		ModelName:  "gpt-5",
	})
	quotaErr := relaykittypes.WithOpenAIError(relaykittypes.OpenAIError{
		Message: "insufficient quota",
		Type:    "insufficient_quota",
		Code:    "insufficient_quota",
	}, http.StatusTooManyRequests)
	usageLimitErr := relaykittypes.WithOpenAIError(relaykittypes.OpenAIError{
		Message: "You've hit your usage limit. Try again later.",
	}, http.StatusTooManyRequests)

	require.False(t, ShouldSkipRetryAfterChannelAffinityError(ctx, quotaErr))
	require.False(t, ShouldSkipRetryAfterChannelAffinityError(ctx, usageLimitErr))

	setting.RetryOnChannelQuotaExceeded = false
	require.True(t, ShouldSkipRetryAfterChannelAffinityError(ctx, quotaErr))
	require.True(t, ShouldSkipRetryAfterChannelAffinityError(ctx, usageLimitErr))
}

func TestShouldSkipRetryAfterChannelAffinityError_UnrelatedErrorStillSkips(t *testing.T) {
	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		RuleName:   "rule-other-error",
		SkipRetry:  true,
		UsingGroup: "default",
		ModelName:  "gpt-5",
	})
	otherErr := relaykittypes.NewErrorWithStatusCode(errors.New("upstream timeout"), relaykittypes.ErrorCodeDoRequestFailed, http.StatusGatewayTimeout)
	require.True(t, ShouldSkipRetryAfterChannelAffinityError(ctx, otherErr))
}

func TestChannelAffinityHitCodexTemplatePassHeadersEffective(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.DB.Exec("DELETE FROM abilities WHERE channel_id = ?", 9527)
		model.DB.Exec("DELETE FROM channels WHERE id = ?", 9527)
		model.InitChannelCache()
	})

	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     9527,
		Name:   "codex-hit-channel",
		Key:    "sk-hit",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "gpt-5",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     "default",
		Model:     "gpt-5",
		ChannelId: 9527,
		Enabled:   true,
	}).Error)
	model.InitChannelCache()

	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	var codexRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "codex cli trace") {
			codexRule = rule
			break
		}
	}
	require.NotNil(t, codexRule)

	affinityValue := fmt.Sprintf("pc-hit-%d", time.Now().UnixNano())
	cacheKeySuffix := buildChannelAffinityCacheKeySuffix(*codexRule, "gpt-5", "default", affinityValue)

	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(cacheKeySuffix, 9527, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{cacheKeySuffix})
	})

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"prompt_cache_key":"%s"}`, affinityValue)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	channelID, found := GetPreferredChannelByAffinity(ctx, "gpt-5", "default")
	require.True(t, found)
	require.Equal(t, 9527, channelID)

	baseOverride := map[string]any{
		"temperature": 0.2,
	}
	mergedOverride, applied := ApplyChannelAffinityOverrideTemplate(ctx, baseOverride)
	require.True(t, applied)
	require.Equal(t, 0.2, mergedOverride["temperature"])

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
			"User-Agent": "codex-cli-test",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: mergedOverride,
			HeadersOverride: map[string]any{
				"X-Static": "legacy-static",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-5"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)

	require.Equal(t, "legacy-static", info.RuntimeHeadersOverride["x-static"])
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	require.Equal(t, "codex-cli-test", info.RuntimeHeadersOverride["user-agent"])

	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	_, exists = info.RuntimeHeadersOverride["x-codex-turn-metadata"]
	require.False(t, exists)
}

func TestGetPreferredChannelByAffinity_InvalidatesDisabledChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.DB.Exec("DELETE FROM channels WHERE id = ?", 9528)
		model.InitChannelCache()
	})

	disabledChannelID := 9528
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     disabledChannelID,
		Name:   "disabled-codex-channel",
		Key:    "sk-disabled",
		Status: common.ChannelStatusAutoDisabled,
	}).Error)
	model.InitChannelCache()

	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	var codexRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "codex cli trace") {
			codexRule = rule
			break
		}
	}
	require.NotNil(t, codexRule)

	affinityValue := fmt.Sprintf("pc-disabled-%d", time.Now().UnixNano())
	cacheKeySuffix := buildChannelAffinityCacheKeySuffix(*codexRule, "gpt-5", "default", affinityValue)

	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(cacheKeySuffix, disabledChannelID, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{cacheKeySuffix})
	})

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"prompt_cache_key":"%s"}`, affinityValue)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	channelID, found := GetPreferredChannelByAffinity(ctx, "gpt-5", "default")
	require.False(t, found)
	require.Equal(t, 0, channelID)

	_, stillFound, err := cache.Get(cacheKeySuffix)
	require.NoError(t, err)
	require.False(t, stillFound)
}

func TestGetPreferredChannelByAffinity_StaleCacheInvalidationCanBeDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)
	originalInvalidateStaleCacheEnabled := setting.InvalidateStaleCacheEnabled
	setting.InvalidateStaleCacheEnabled = false
	t.Cleanup(func() {
		setting.InvalidateStaleCacheEnabled = originalInvalidateStaleCacheEnabled
	})

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.DB.Exec("DELETE FROM channels WHERE id = ?", 9530)
		model.InitChannelCache()
	})

	disabledChannelID := 9530
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     disabledChannelID,
		Name:   "disabled-codex-channel-with-toggle-off",
		Key:    "sk-disabled-toggle-off",
		Status: common.ChannelStatusAutoDisabled,
	}).Error)
	model.InitChannelCache()

	var codexRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "codex cli trace") {
			codexRule = rule
			break
		}
	}
	require.NotNil(t, codexRule)

	affinityValue := fmt.Sprintf("pc-disabled-toggle-off-%d", time.Now().UnixNano())
	cacheKeySuffix := buildChannelAffinityCacheKeySuffix(*codexRule, "gpt-5.4", "default", affinityValue)

	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(cacheKeySuffix, disabledChannelID, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{cacheKeySuffix})
	})

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"prompt_cache_key":"%s"}`, affinityValue)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	channelID, found := GetPreferredChannelByAffinity(ctx, "gpt-5.4", "default")
	require.True(t, found)
	require.Equal(t, disabledChannelID, channelID)

	_, stillFound, err := cache.Get(cacheKeySuffix)
	require.NoError(t, err)
	require.True(t, stillFound)
}

func TestGetPreferredChannelByAffinity_InvalidatesChannelNoLongerEnabledForGroupModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		model.DB.Exec("DELETE FROM abilities WHERE channel_id = ?", 9529)
		model.DB.Exec("DELETE FROM channels WHERE id = ?", 9529)
		model.InitChannelCache()
	})

	channelID := 9529
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     channelID,
		Name:   "orphaned-codex-channel",
		Key:    "sk-orphaned",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "gpt-5.4-mini",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     "default",
		Model:     "gpt-5.4-mini",
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
	model.InitChannelCache()

	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	var codexRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "codex cli trace") {
			codexRule = rule
			break
		}
	}
	require.NotNil(t, codexRule)

	affinityValue := fmt.Sprintf("pc-orphaned-%d", time.Now().UnixNano())
	cacheKeySuffix := buildChannelAffinityCacheKeySuffix(*codexRule, "gpt-5.4", "default", affinityValue)

	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(cacheKeySuffix, channelID, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{cacheKeySuffix})
	})

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"prompt_cache_key":"%s"}`, affinityValue)))
	ctx.Request.Header.Set("Content-Type", "application/json")

	preferredChannelID, found := GetPreferredChannelByAffinity(ctx, "gpt-5.4", "default")
	require.False(t, found)
	require.Equal(t, 0, preferredChannelID)

	_, stillFound, err := cache.Get(cacheKeySuffix)
	require.NoError(t, err)
	require.False(t, stillFound)
}

func TestClaudeTemplatePassesHeadersWithoutPromptCacheKeyMutation(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	var claudeRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "claude cli trace") {
			claudeRule = rule
			break
		}
	}
	require.NotNil(t, claudeRule)

	meta := channelAffinityMeta{
		RuleName:      claudeRule.Name,
		ParamTemplate: claudeRule.ParamOverrideTemplate,
		KeyValue:      "claude-session-123",
	}
	ctx := buildChannelAffinityTemplateContextForTest(meta)

	mergedOverride, applied := ApplyChannelAffinityOverrideTemplate(ctx, map[string]interface{}{})
	require.True(t, applied)

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{
			"X-Claude-Code-Session-Id": "claude-session-123",
			"User-Agent":               "claude-cli-test",
			"X-App":                    "cli",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride:        mergedOverride,
			ParamOverrideContext: buildChannelAffinityParamOverrideContext(meta),
		},
	}

	out, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"claude-opus-4-8"}`), info)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"claude-opus-4-8"}`, string(out))

	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "claude-session-123", info.RuntimeHeadersOverride["x-claude-code-session-id"])
	require.Empty(t, info.RuntimeHeadersOverride["session_id"])
	require.Equal(t, "claude-cli-test", info.RuntimeHeadersOverride["user-agent"])
	require.Equal(t, "cli", info.RuntimeHeadersOverride["x-app"])
}

func TestClaudeCodexCompatTemplateSyncsClaudeSessionToPromptCacheKeyAndSessionHeader(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	var compatRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if strings.EqualFold(strings.TrimSpace(rule.Name), "claude cli codex compat trace") {
			compatRule = rule
			break
		}
	}
	require.NotNil(t, compatRule)

	meta := channelAffinityMeta{
		RuleName:      compatRule.Name,
		ParamTemplate: compatRule.ParamOverrideTemplate,
		KeyValue:      "claude-session-123",
	}
	ctx := buildChannelAffinityTemplateContextForTest(meta)

	mergedOverride, applied := ApplyChannelAffinityOverrideTemplate(ctx, map[string]interface{}{})
	require.True(t, applied)

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{
			"X-Claude-Code-Session-Id": "claude-session-123",
			"User-Agent":               "claude-cli-test",
			"X-App":                    "cli",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride:        mergedOverride,
			ParamOverrideContext: buildChannelAffinityParamOverrideContext(meta),
		},
	}

	out, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-5.4"}`), info)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-5.4","prompt_cache_key":"claude-session-123"}`, string(out))

	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "claude-session-123", info.RuntimeHeadersOverride["x-claude-code-session-id"])
	require.Equal(t, "claude-session-123", info.RuntimeHeadersOverride["session_id"])
	require.Equal(t, "claude-cli-test", info.RuntimeHeadersOverride["user-agent"])
	require.Equal(t, "cli", info.RuntimeHeadersOverride["x-app"])
}

func TestExtractChannelAffinityValueFromRequestHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")

	value := extractChannelAffinityValue(ctx, operation_setting.ChannelAffinityKeySource{
		Type: "request_header",
		Key:  "X-Claude-Code-Session-Id",
	})
	require.Equal(t, "header-session", value)
}

func TestExtractChannelAffinityValueFromNestedJSONString(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/messages",
		strings.NewReader(`{"metadata":{"user_id":"{\"device_id\":\"dev-1\",\"session_id\":\"nested-session\"}"}}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	value := extractChannelAffinityValue(ctx, operation_setting.ChannelAffinityKeySource{
		Type:       "gjson",
		Path:       "metadata.user_id",
		NestedPath: "session_id",
	})
	require.Equal(t, "nested-session", value)
}

func TestMidjourneyPolicyAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		code       int
		properties map[string]any
		requestErr error
		accepted   bool
		reason     string
	}{
		{name: "submitted", status: 200, code: 1, accepted: true},
		{name: "existing task", status: 200, code: 21, accepted: true},
		{name: "queued", status: 200, code: 22, accepted: true},
		{name: "HTTP 200 with rejected prompt", status: 200, code: 24, reason: "non_retryable_error"},
		{name: "failed existing task", status: 200, code: 21, properties: map[string]any{"status": "FAILURE"}, reason: "task_accepted"},
		{name: "HTTP failure", status: 502, code: 1, reason: "non_retryable_error"},
		{name: "ambiguous transport failure", status: 502, requestErr: errors.New("connection reset"), reason: "non_retryable_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
			ctx.Set("channel_id", 7)
			state := RequestPolicy(ctx)
			state.BeginAttempt(&model.Channel{Id: 7}, "default")
			response := &dto.MidjourneyResponseWithStatusCode{StatusCode: tc.status, Response: dto.MidjourneyResponse{Code: tc.code, Properties: tc.properties}}
			accepted := RecordMidjourneyPolicyResponse(ctx, response, tc.requestErr)
			assert.Equal(t, tc.accepted, accepted)
			assert.False(t, state.Successful, "acceptance alone must not bind before local processing finishes")
			if accepted {
				MarkRequestPolicySuccess(ctx, nil)
				assert.True(t, state.Successful)
				return
			}
			events := state.Events()
			require.Len(t, events, 3)
			assert.Equal(t, PolicyDecision{Action: "failure", Reason: "upstream_failure", Source: "upstream"}, events[1].Decision)
			assert.Equal(t, tc.status, events[1].Status)
			assert.Equal(t, PolicyDecision{Action: "stop", Reason: tc.reason, Source: "system"}, events[2].Decision, "submissions are never replayed")
		})
	}
}

func TestSessionRulesInheritOrOverrideGlobalDefault(t *testing.T) {
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	for _, globalMode := range []string{"", "off", "prefer", "strict"} {
		for _, tc := range []struct {
			name, mode, expected string
			skipRetry            bool
		}{
			{name: "inherit", mode: "inherit", expected: globalMode},
			{name: "override-strict", mode: "strict", expected: "strict"},
			{name: "override-prefer", mode: "prefer", expected: "prefer", skipRetry: true},
			{name: "override-off", mode: "off", expected: "off", skipRetry: true},
			{name: "legacy-strict", expected: "strict", skipRetry: true},
			{name: "legacy-prefer", expected: "prefer"},
		} {
			t.Run(globalMode+"/"+tc.name, func(t *testing.T) {
				expected := tc.expected
				if expected == "" {
					expected = "prefer"
				}
				source := "session_rule"
				if tc.mode == "inherit" {
					source = "global"
				}
				rule := operation_setting.ChannelAffinityRule{
					Name: tc.name, ModelRegex: []string{"^test-model$"},
					KeySources:  []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}},
					SessionMode: tc.mode, SkipRetryOnFailure: tc.skipRetry,
					ParamOverrideTemplate: map[string]any{"temperature": 0}, IncludeRuleName: true,
				}
				encoded, err := common.Marshal([]operation_setting.ChannelAffinityRule{rule})
				require.NoError(t, err)
				snapshot, err := model.BuildRequestPolicy(map[string]string{"RetryTimes": "2", "channel_affinity_setting.enabled": "true", "channel_affinity_setting.session_mode": globalMode, "channel_affinity_setting.rules": string(encoded), "channel_affinity_setting.invalidate_stale_cache_enabled": "false"})
				require.NoError(t, err)
				*setting = snapshot.Affinity
				key := t.Name()
				cacheKey := buildChannelAffinityCacheKeySuffix(rule, "test-model", "default", key)
				cache := getChannelAffinityCache()
				require.NoError(t, cache.SetWithTTL(cacheKey, 1, time.Minute))
				t.Cleanup(func() { _, err := cache.DeleteMany([]string{cacheKey}); assert.NoError(t, err) })
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				ctx.Request.Header.Set("X-Session", key)
				state := RequestPolicy(ctx)
				channelID, found := GetPreferredChannelByAffinity(ctx, "test-model", "default")
				assert.Equal(t, expected != "off", found)
				if found {
					assert.Equal(t, 1, channelID)
				}
				assert.Equal(t, expected, state.SessionMode)
				assert.Equal(t, source, state.SessionModeSource)
				assert.Equal(t, expected == "strict", ShouldSkipRetryAfterChannelAffinityFailure(ctx))
				decision := DecideRelayRetry(ctx, types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), 2)
				if expected == "strict" {
					assert.Equal(t, PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}, decision)
				} else {
					assert.Equal(t, "retry", decision.Action)
				}
				events := state.Events()
				require.Len(t, events, 1)
				assert.Equal(t, PolicyDecision{Action: "match", Reason: "session_rule_matched", Source: "session_rule"}, events[0].Decision)
				assert.Equal(t, tc.name, events[0].Rule)
				transformed, applied := ApplyChannelAffinityOverrideTemplate(ctx, nil)
				assert.True(t, applied, "session behavior does not disable request transforms")
				assert.EqualValues(t, 0, transformed["temperature"])
				if expected == "off" {
					ctx.Set("channel_id", 9)
					RecordChannelAffinity(ctx, 9)
					cached, found, err := cache.Get(cacheKey)
					require.NoError(t, err)
					assert.True(t, found)
					assert.Equal(t, 1, cached, "off does not refresh session bindings")
					_, err = cache.DeleteMany([]string{cacheKey})
					require.NoError(t, err)
					RecordChannelAffinity(ctx, 9)
					_, found, err = cache.Get(cacheKey)
					require.NoError(t, err)
					assert.False(t, found, "off does not establish session bindings")
				}
			})
		}
	}
}
