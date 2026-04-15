package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

func TestParseCodexBackendModels(t *testing.T) {
	t.Run("parses object envelope", func(t *testing.T) {
		body := []byte(`{
			"models": [
				{"slug": "gpt-5.4"},
				{"id": "gpt-5.1-codex"},
				{"slug": "gpt-5.4"}
			]
		}`)
		got := parseCodexBackendModels(body)
		want := []string{"gpt-5.4", "gpt-5.1-codex"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parseCodexBackendModels() = %v, want %v", got, want)
		}
	})

	t.Run("parses array payload", func(t *testing.T) {
		body := []byte(`[
			{"slug": "gpt-5"},
			{"id": "gpt-5.4"}
		]`)
		got := parseCodexBackendModels(body)
		want := []string{"gpt-5", "gpt-5.4"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("parseCodexBackendModels() = %v, want %v", got, want)
		}
	})
}

func TestFilterCodexCompatibleModels(t *testing.T) {
	t.Run("keeps builtin codex-compatible models", func(t *testing.T) {
		got := filterCodexCompatibleModels([]string{"gpt-4o", "gpt-5.4", "gpt-5.1-codex"})
		want := []string{"gpt-5.4", "gpt-5.1-codex"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("filterCodexCompatibleModels() = %v, want %v", got, want)
		}
	})

	t.Run("falls back to gpt5 and codex family when builtin match is empty", func(t *testing.T) {
		got := filterCodexCompatibleModels([]string{"gpt-4o", "gpt-5-experimental", "my-codex-preview"})
		want := []string{"gpt-5-experimental", "my-codex-preview"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("filterCodexCompatibleModels() = %v, want %v", got, want)
		}
	})
}

func TestBuildCodexModelDiscoveryURL(t *testing.T) {
	got, err := buildCodexModelDiscoveryURL("https://chatgpt.com/", "/backend-api/models")
	if err != nil {
		t.Fatalf("buildCodexModelDiscoveryURL() error = %v", err)
	}
	if got != "https://chatgpt.com/backend-api/models" {
		t.Fatalf("buildCodexModelDiscoveryURL() = %q, want %q", got, "https://chatgpt.com/backend-api/models")
	}

	got, err = buildCodexModelDiscoveryURL("https://chatgpt.com", "https://proxy.example.com/v1/models")
	if err != nil {
		t.Fatalf("buildCodexModelDiscoveryURL() full url error = %v", err)
	}
	if got != "https://proxy.example.com/v1/models" {
		t.Fatalf("buildCodexModelDiscoveryURL() = %q, want full url passthrough", got)
	}
}

func TestFetchCodexUpstreamModelLists(t *testing.T) {
	settings := system_setting.GetCodexModelDiscoverySetting()
	originalChatGPTPath := settings.ChatGPTModelsPath
	originalOpenAIPath := settings.OpenAIModelsPath
	settings.ChatGPTModelsPath = "/backend-api/models"
	settings.OpenAIModelsPath = "/v1/models"
	defer func() {
		settings.ChatGPTModelsPath = originalChatGPTPath
		settings.OpenAIModelsPath = originalOpenAIPath
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/models":
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.4"},{"slug":"gpt-4o"}]}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.1-codex"},{"id":"gpt-4o-mini"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	got, err := FetchCodexUpstreamModelLists(
		context.Background(),
		server.Client(),
		server.URL,
		"access-token",
		"account-id",
	)
	if err != nil {
		t.Fatalf("FetchCodexUpstreamModelLists() error = %v", err)
	}

	if !reflect.DeepEqual(got.ChatGPTModels, []string{"gpt-5.4", "gpt-4o"}) {
		t.Fatalf("ChatGPTModels = %v", got.ChatGPTModels)
	}
	if !reflect.DeepEqual(got.APIModels, []string{"gpt-5.1-codex", "gpt-4o-mini"}) {
		t.Fatalf("APIModels = %v", got.APIModels)
	}
	if !reflect.DeepEqual(got.ReferenceModels, []string{"gpt-5.4", "gpt-4o"}) {
		t.Fatalf("ReferenceModels = %v", got.ReferenceModels)
	}
	if got.ReferenceLabel != "ChatGPT 可见模型" {
		t.Fatalf("ReferenceLabel = %q", got.ReferenceLabel)
	}
	if !reflect.DeepEqual(got.CodexModels, []string{"gpt-5.4", "gpt-5.1-codex"}) {
		t.Fatalf("CodexModels = %v", got.CodexModels)
	}
}
