package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOaiResponsesStreamHandlerReturnsErrorWhenCompletedEventMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = oldTimeout
	})
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n",
		)),
	}

	usage, err := OaiResponsesStreamHandler(ctx, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.4",
		},
	}, resp)

	require.Nil(t, usage)
	require.Error(t, err)
	require.True(t, types.IsSkipRetryError(err))
	require.Contains(t, err.Error(), "responses stream disconnected before completion")
}

func TestFinalizeResponsesStreamMarksCompletedEventAsDone(t *testing.T) {
	info := &relaycommon.RelayInfo{
		StreamStatus: &relaycommon.StreamStatus{
			EndReason: relaycommon.StreamEndReasonEOF,
			EndError:  io.EOF,
		},
	}

	err := finalizeResponsesStream(info, true)

	require.Nil(t, err)
	require.NotNil(t, info.StreamStatus)
	require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	require.NoError(t, info.StreamStatus.EndError)
}
