package service

import (
	"reflect"
	"testing"
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
