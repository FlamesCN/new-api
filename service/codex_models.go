package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/samber/lo"
)

type CodexUpstreamModelLists struct {
	ChatGPTModels   []string `json:"chatgpt_models"`
	APIModels       []string `json:"api_models"`
	ReferenceModels []string `json:"reference_models"`
	ReferenceLabel  string   `json:"reference_label"`
	CodexModels     []string `json:"codex_models"`
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

func buildCodexModelDiscoveryURL(baseURL string, endpoint string) (string, error) {
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return "", fmt.Errorf("empty baseURL")
	}
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return "", fmt.Errorf("empty endpoint")
	}
	if strings.HasPrefix(ep, "http://") || strings.HasPrefix(ep, "https://") {
		return ep, nil
	}
	if !strings.HasPrefix(ep, "/") {
		ep = "/" + ep
	}
	return bu + ep, nil
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
	url, err := buildCodexModelDiscoveryURL(baseURL, path)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

func pickCodexReferenceModels(chatgptModels []string, apiModels []string) ([]string, string) {
	if len(chatgptModels) > 0 {
		return normalizeCodexModelNames(chatgptModels), "ChatGPT 可见模型"
	}
	if len(apiModels) > 0 {
		return normalizeCodexModelNames(apiModels), "OpenAI /v1/models 模型"
	}
	return nil, "上游模型"
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

	settings := system_setting.GetCodexModelDiscoverySetting()
	chatgptPath := strings.TrimSpace(settings.ChatGPTModelsPath)
	openAIPath := strings.TrimSpace(settings.OpenAIModelsPath)

	var (
		chatgptModels []string
		apiModels     []string
		errMessages   []string
	)

	if chatgptPath != "" {
		models, err := fetchCodexBackendModelsOnce(ctx, client, baseURL, accessToken, accountID, chatgptPath)
		if err != nil {
			errMessages = append(errMessages, fmt.Sprintf("chatgpt source %q failed: %v", chatgptPath, err))
		} else {
			chatgptModels = models
		}
	}
	if openAIPath != "" {
		models, err := fetchCodexBackendModelsOnce(ctx, client, baseURL, accessToken, accountID, openAIPath)
		if err != nil {
			errMessages = append(errMessages, fmt.Sprintf("openai source %q failed: %v", openAIPath, err))
		} else {
			apiModels = models
		}
	}

	if len(chatgptModels) == 0 && len(apiModels) == 0 {
		if len(errMessages) == 0 {
			return nil, fmt.Errorf("failed to fetch codex models")
		}
		return nil, errors.New(strings.Join(errMessages, "; "))
	}

	referenceModels, referenceLabel := pickCodexReferenceModels(chatgptModels, apiModels)
	codexModels := filterCodexCompatibleModels(
		normalizeCodexModelNames(append(chatgptModels, apiModels...)),
	)

	return &CodexUpstreamModelLists{
		ChatGPTModels:   normalizeCodexModelNames(chatgptModels),
		APIModels:       normalizeCodexModelNames(apiModels),
		ReferenceModels: referenceModels,
		ReferenceLabel:  referenceLabel,
		CodexModels:     codexModels,
	}, nil
}

const (
	codexLatestReleaseURL      = "https://api.github.com/repos/openai/codex/releases/latest"
	codexClientVersionCacheTTL = time.Hour
)

type codexClientVersionCache struct {
	sync.Mutex
	version   string
	expiresAt time.Time
}

var latestCodexClientVersion codexClientVersionCache

func GetLatestCodexClientVersion(ctx context.Context, client *http.Client) (string, error) {
	return latestCodexClientVersion.get(ctx, client, codexLatestReleaseURL, time.Now())
}

func (cache *codexClientVersionCache) get(ctx context.Context, client *http.Client, releaseURL string, now time.Time) (string, error) {
	cache.Lock()
	defer cache.Unlock()

	if cache.version != "" && now.Before(cache.expiresAt) {
		return cache.version, nil
	}

	version, err := fetchLatestCodexClientVersion(ctx, client, releaseURL)
	if err != nil {
		if cache.version != "" {
			cache.expiresAt = now.Add(codexClientVersionCacheTTL)
			return cache.version, nil
		}
		return "", err
	}

	cache.version = version
	cache.expiresAt = now.Add(codexClientVersionCacheTTL)
	return version, nil
}

func fetchLatestCodexClientVersion(ctx context.Context, client *http.Client, releaseURL string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("nil http client")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "new-api")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("codex release lookup failed: status=%d", resp.StatusCode)
	}

	var release struct {
		Name       string `json:"name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := common.DecodeJson(resp.Body, &release); err != nil {
		return "", err
	}
	if release.Draft || release.Prerelease {
		return "", fmt.Errorf("latest codex release is not stable")
	}
	version := strings.TrimSpace(release.Name)
	if version == "" {
		return "", fmt.Errorf("latest codex release has no version name")
	}
	return version, nil
}

func FetchCodexModels(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	oauthKey *CodexOAuthKey,
	clientVersion string,
) (statusCode int, models []string, err error) {
	if client == nil {
		return 0, nil, fmt.Errorf("nil http client")
	}
	if oauthKey == nil {
		return 0, nil, fmt.Errorf("nil oauth key")
	}

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	clientVersion = strings.TrimSpace(clientVersion)
	if baseURL == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	if accessToken == "" {
		return 0, nil, fmt.Errorf("codex channel: access_token is required")
	}
	if accountID == "" {
		return 0, nil, fmt.Errorf("codex channel: account_id is required")
	}
	if clientVersion == "" {
		return 0, nil, fmt.Errorf("codex channel: client_version is required")
	}

	modelsURL, err := url.Parse(baseURL + "/backend-api/codex/models")
	if err != nil {
		return 0, nil, err
	}
	query := modelsURL.Query()
	query.Set("client_version", clientVersion)
	modelsURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("User-Agent", "codex-cli/"+clientVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, nil, nil
	}

	var result struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return resp.StatusCode, nil, err
	}

	seen := make(map[string]struct{}, len(result.Models))
	models = make([]string, 0, len(result.Models))
	for _, item := range result.Models {
		slug := strings.TrimSpace(item.Slug)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		models = append(models, slug)
	}
	return resp.StatusCode, models, nil
}
