package deepseek

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	relaykitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestPassesThrough(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(
		nil,
		&relaycommon.RelayInfo{},
		relaykitdto.OpenAIResponsesRequest{Model: "deepseek-v4-flash"},
	)

	require.NoError(t, err)
	request, ok := converted.(relaykitdto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.Equal(t, "deepseek-v4-flash", request.Model)
}

func TestGetRequestURLUsesResponsesEndpoint(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.deepseek.com",
		},
		RelayMode: relayconstant.RelayModeResponses,
	}

	url, err := adaptor.GetRequestURL(info)

	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/v1/responses", url)
}

func TestGetRequestURLKeepsChatCompletionsEndpoint(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.deepseek.com",
		},
		RelayMode: relayconstant.RelayModeChatCompletions,
	}

	url, err := adaptor.GetRequestURL(info)

	require.NoError(t, err)
	require.Equal(t, "https://api.deepseek.com/v1/chat/completions", url)
}
