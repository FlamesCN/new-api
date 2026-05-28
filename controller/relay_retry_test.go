package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryStopsSingleChannel429(t *testing.T) {
	oldCounter := countEnabledChannelsForRetry
	countEnabledChannelsForRetry = func(group string, model string) int {
		require.Equal(t, "dc-gemini", group)
		require.Equal(t, "gemini-3.1-pro-preview", model)
		return 1
	}
	t.Cleanup(func() {
		countEnabledChannelsForRetry = oldCounter
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "dc-gemini")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "dc-gemini")
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gemini-3.1-pro-preview")

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "You have exhausted your capacity on this model.",
		Type:    "upstream_error",
		Code:    429,
	}, http.StatusTooManyRequests)

	require.False(t, shouldRetry(ctx, err, 5))
}

func TestShouldRetryKeepsMultiChannel429Retries(t *testing.T) {
	oldCounter := countEnabledChannelsForRetry
	countEnabledChannelsForRetry = func(group string, model string) int {
		require.Equal(t, "dc-gemini", group)
		require.Equal(t, "gemini-3.1-pro-preview", model)
		return 2
	}
	t.Cleanup(func() {
		countEnabledChannelsForRetry = oldCounter
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "dc-gemini")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "dc-gemini")
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gemini-3.1-pro-preview")

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "You have exhausted your capacity on this model.",
		Type:    "upstream_error",
		Code:    429,
	}, http.StatusTooManyRequests)

	require.True(t, shouldRetry(ctx, err, 5))
}

func TestShouldRetryPreservesAutoGroupFallbackFor429(t *testing.T) {
	oldCounter := countEnabledChannelsForRetry
	countEnabledChannelsForRetry = func(group string, model string) int {
		require.Equal(t, "dc-gemini", group)
		require.Equal(t, "gemini-3.1-pro-preview", model)
		return 1
	}
	t.Cleanup(func() {
		countEnabledChannelsForRetry = oldCounter
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "dc-gemini")
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gemini-3.1-pro-preview")

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "You have exhausted your capacity on this model.",
		Type:    "upstream_error",
		Code:    429,
	}, http.StatusTooManyRequests)

	require.True(t, shouldRetry(ctx, err, 5))
}
