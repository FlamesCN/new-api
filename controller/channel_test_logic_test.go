package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
)

func TestResolveChannelTestStream(t *testing.T) {
	settingsBytes, err := common.Marshal(dto.ChannelOtherSettings{
		TestStreamEnabled: true,
	})
	if err != nil {
		t.Fatalf("marshal settings failed: %v", err)
	}

	channel := &model.Channel{OtherSettings: string(settingsBytes)}
	if !resolveChannelTestStream(channel, nil) {
		t.Fatal("expected channel default stream test setting to be used when override is nil")
	}

	overrideFalse := false
	if resolveChannelTestStream(channel, &overrideFalse) {
		t.Fatal("expected explicit false override to disable stream test")
	}

	overrideTrue := true
	if !resolveChannelTestStream(channel, &overrideTrue) {
		t.Fatal("expected explicit true override to enable stream test")
	}

	codexChannel := &model.Channel{Type: constant.ChannelTypeCodex}
	if !resolveChannelTestStream(codexChannel, nil) {
		t.Fatal("expected codex channels without stored setting to default to stream test")
	}

	codexChannelWithExplicitFalse := &model.Channel{
		Type:          constant.ChannelTypeCodex,
		OtherSettings: `{"test_stream_enabled":false}`,
	}
	if resolveChannelTestStream(codexChannelWithExplicitFalse, nil) {
		t.Fatal("expected explicit false stream setting to override codex default")
	}

	nonCodexChannel := &model.Channel{Type: 1}
	if resolveChannelTestStream(nonCodexChannel, nil) {
		t.Fatal("expected non-codex channels without stored setting to default to non-stream test")
	}
}

func TestShouldSkipChannelAutoTest(t *testing.T) {
	tests := []struct {
		name                string
		channel             *model.Channel
		includeAutoDisabled bool
		want                bool
	}{
		{
			name:                "nil channel",
			channel:             nil,
			includeAutoDisabled: true,
			want:                true,
		},
		{
			name: "manual disabled always skipped",
			channel: &model.Channel{
				Status: common.ChannelStatusManuallyDisabled,
			},
			includeAutoDisabled: true,
			want:                true,
		},
		{
			name: "auto disabled skipped when disabled in monitor setting",
			channel: &model.Channel{
				Status: common.ChannelStatusAutoDisabled,
			},
			includeAutoDisabled: false,
			want:                true,
		},
		{
			name: "auto disabled included when enabled in monitor setting",
			channel: &model.Channel{
				Status: common.ChannelStatusAutoDisabled,
			},
			includeAutoDisabled: true,
			want:                false,
		},
		{
			name: "enabled channel is included",
			channel: &model.Channel{
				Status: common.ChannelStatusEnabled,
			},
			includeAutoDisabled: false,
			want:                false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipChannelAutoTest(tt.channel, tt.includeAutoDisabled)
			if got != tt.want {
				t.Fatalf("shouldSkipChannelAutoTest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveChannelTestModels(t *testing.T) {
	channel := &model.Channel{
		Type:   constant.ChannelTypeCodex,
		Models: "gpt-5,gpt-5.4,gpt-5.1-codex",
	}
	channel.TestModel = common.GetPointer("gpt-5, gpt-5.4")

	t.Run("explicit request model list stays scoped to request", func(t *testing.T) {
		got := resolveChannelTestModels(channel, "gpt-5, gpt-5.1-codex")
		want := []string{"gpt-5", "gpt-5.1-codex"}
		if len(got) != len(want) {
			t.Fatalf("resolveChannelTestModels() len = %d, want %d, got=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("resolveChannelTestModels()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
			}
		}
	})

	t.Run("channel test model falls through to channel models without duplicates", func(t *testing.T) {
		got := resolveChannelTestModels(channel, "")
		want := []string{"gpt-5", "gpt-5.4", "gpt-5.1-codex"}
		if len(got) != len(want) {
			t.Fatalf("resolveChannelTestModels() len = %d, want %d, got=%v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("resolveChannelTestModels()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
			}
		}
	})

	t.Run("empty config falls back to gpt-4o-mini", func(t *testing.T) {
		got := resolveChannelTestModels(&model.Channel{}, "")
		if len(got) != 1 || got[0] != "gpt-4o-mini" {
			t.Fatalf("resolveChannelTestModels() = %v, want [gpt-4o-mini]", got)
		}
	})
}

func TestShouldRetryChannelTestWithNextModel(t *testing.T) {
	retryable := types.WithOpenAIError(types.OpenAIError{
		Message: "The 'gpt-5' model is not supported when using Codex with a ChatGPT account.",
		Type:    "invalid_request_error",
		Code:    "model_not_found",
	}, http.StatusBadRequest)
	if !shouldRetryChannelTestWithNextModel(retryable) {
		t.Fatal("expected unsupported model error to trigger fallback")
	}

	nonRetryable := types.WithOpenAIError(types.OpenAIError{
		Message: "account_deactivated",
		Type:    "invalid_request_error",
		Code:    "account_deactivated",
	}, http.StatusForbidden)
	if shouldRetryChannelTestWithNextModel(nonRetryable) {
		t.Fatal("expected account-level failure to stop fallback")
	}

	channelErr := types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	if shouldRetryChannelTestWithNextModel(channelErr) {
		t.Fatal("expected channel-level errors to stop fallback")
	}
}
