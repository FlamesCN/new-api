package system_setting

import "github.com/QuantumNous/new-api/setting/config"

type CodexModelDiscoverySetting struct {
	ChatGPTModelsPath string `json:"chatgpt_models_path"`
	OpenAIModelsPath  string `json:"openai_models_path"`
}

var defaultCodexModelDiscoverySetting = CodexModelDiscoverySetting{
	ChatGPTModelsPath: "/backend-api/models",
	OpenAIModelsPath:  "/v1/models",
}

func init() {
	config.GlobalConfig.Register("codex_model_discovery", &defaultCodexModelDiscoverySetting)
}

func GetCodexModelDiscoverySetting() *CodexModelDiscoverySetting {
	return &defaultCodexModelDiscoverySetting
}
