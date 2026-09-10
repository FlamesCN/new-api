package reasoning

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDeepSeekV4ThinkingSuffix(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantBase     string
		wantThinking string
		wantEffort   string
		wantOK       bool
	}{
		{name: "v4 flash max", model: "deepseek-v4-flash-max", wantBase: "deepseek-v4-flash", wantThinking: "enabled", wantEffort: "max", wantOK: true},
		{name: "v4 pro none", model: "deepseek-v4-pro-none", wantBase: "deepseek-v4-pro", wantThinking: "disabled", wantEffort: "", wantOK: true},
		{name: "v4.1 flash max", model: "deepseek-v4.1-flash-max", wantBase: "deepseek-v4.1-flash", wantThinking: "enabled", wantEffort: "max", wantOK: true},
		{name: "v4.1 flash none", model: "deepseek-v4.1-flash-none", wantBase: "deepseek-v4.1-flash", wantThinking: "disabled", wantEffort: "", wantOK: true},
		{name: "v4.1 flash no suffix", model: "deepseek-v4.1-flash", wantBase: "deepseek-v4.1-flash", wantThinking: "", wantEffort: "", wantOK: false},
		{name: "v4 flash no suffix", model: "deepseek-v4-flash", wantBase: "deepseek-v4-flash", wantThinking: "", wantEffort: "", wantOK: false},
		{name: "non v4 model", model: "deepseek-chat-max", wantBase: "deepseek-chat-max", wantThinking: "", wantEffort: "", wantOK: false},
		{name: "unrelated model", model: "gpt-4.1-max", wantBase: "gpt-4.1-max", wantThinking: "", wantEffort: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, thinking, effort, ok := ParseDeepSeekV4ThinkingSuffix(tt.model)
			require.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantBase, base)
			assert.Equal(t, tt.wantThinking, thinking)
			assert.Equal(t, tt.wantEffort, effort)
		})
	}
}
