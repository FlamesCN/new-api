package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/samber/lo"
)

type CodexUpstreamModelLists struct {
	ChatGPTModels []string `json:"chatgpt_models"`
	CodexModels   []string `json:"codex_models"`
}

var builtinCodexCompatibleModels = []string{
	"gpt-5", "gpt-5-codex", "gpt-5-codex-mini",
	"gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini",
	"gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.3-codex-spark",
	"gpt-5.4",
}

type codexBackendModelsEnvelope struct {
	Data []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"data"`
	Models []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	} `json:"models"`
}

func normalizeCodexModelNames(models []string) []string {
	return lo.Uniq(lo.FilterMap(models, func(model string, _ int) (string, bool) {
		trimmed := strings.TrimSpace(model)
		return trimmed, trimmed != ""
	}))
}

func parseCodexBackendModels(body []byte) []string {
	var envelope codexBackendModelsEnvelope
	if err := common.Unmarshal(body, &envelope); err == nil {
		models := make([]string, 0, len(envelope.Data)+len(envelope.Models))
		for _, item := range envelope.Data {
			if slug := strings.TrimSpace(item.Slug); slug != "" {
				models = append(models, slug)
				continue
			}
			if id := strings.TrimSpace(item.ID); id != "" {
				models = append(models, id)
			}
		}
		for _, item := range envelope.Models {
			if slug := strings.TrimSpace(item.Slug); slug != "" {
				models = append(models, slug)
				continue
			}
			if id := strings.TrimSpace(item.ID); id != "" {
				models = append(models, id)
			}
		}
		if len(models) > 0 {
			return normalizeCodexModelNames(models)
		}
	}

	var rawModels []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := common.Unmarshal(body, &rawModels); err == nil {
		models := make([]string, 0, len(rawModels))
		for _, item := range rawModels {
			if slug := strings.TrimSpace(item.Slug); slug != "" {
				models = append(models, slug)
				continue
			}
			if id := strings.TrimSpace(item.ID); id != "" {
				models = append(models, id)
			}
		}
		return normalizeCodexModelNames(models)
	}

	return nil
}

func filterCodexCompatibleModels(chatgptModels []string) []string {
	builtinSet := make(map[string]struct{}, len(builtinCodexCompatibleModels))
	for _, modelName := range builtinCodexCompatibleModels {
		builtinSet[modelName] = struct{}{}
	}

	compatible := lo.Filter(normalizeCodexModelNames(chatgptModels), func(modelName string, _ int) bool {
		_, ok := builtinSet[modelName]
		return ok
	})
	if len(compatible) > 0 {
		return compatible
	}

	// ChatGPT account-visible models may omit some Codex-specific aliases.
	// Fall back to GPT-5/Codex-family names rather than returning unrelated ChatGPT-only models.
	return lo.Filter(normalizeCodexModelNames(chatgptModels), func(modelName string, _ int) bool {
		lower := strings.ToLower(modelName)
		return strings.HasPrefix(lower, "gpt-5") || strings.Contains(lower, "codex")
	})
}

func fetchCodexBackendModelsOnce(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
	path string,
) ([]string, error) {
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return nil, fmt.Errorf("empty baseURL")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bu+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("chatgpt-account-id", strings.TrimSpace(accountID))
	req.Header.Set("Accept", "application/json")
	if req.Header.Get("originator") == "" {
		req.Header.Set("originator", "codex_cli_rs")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bad response status code %d, body: %s", resp.StatusCode, common.MaskSensitiveInfo(string(body)))
	}

	models := parseCodexBackendModels(body)
	if len(models) == 0 {
		return nil, fmt.Errorf("no models found in response")
	}
	return models, nil
}

func FetchCodexUpstreamModelLists(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	accountID string,
) (*CodexUpstreamModelLists, error) {
	if client == nil {
		return nil, fmt.Errorf("nil http client")
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("empty accessToken")
	}
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("empty accountID")
	}

	paths := []string{
		"/backend-api/models",
		"/backend-api/models?history_and_training_disabled=false",
	}
	var lastErr error
	for _, path := range paths {
		chatgptModels, err := fetchCodexBackendModelsOnce(ctx, client, baseURL, accessToken, accountID, path)
		if err != nil {
			lastErr = err
			continue
		}
		return &CodexUpstreamModelLists{
			ChatGPTModels: chatgptModels,
			CodexModels:   filterCodexCompatibleModels(chatgptModels),
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to fetch codex models")
	}
	return nil, lastErr
}
