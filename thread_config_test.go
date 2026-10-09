package codexgo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// The per-thread provider overlay must actually reach the wire: a caller declares its provider
// in ThreadStartParams.Config and selects it with ModelProvider, and the app-server merges
// that overlay into its own config for the thread. If either field is dropped in serialisation
// the caller silently gets the server's default provider instead.
func TestThreadStartSendsProviderConfigOverlay(t *testing.T) {
	client, mock := newClientFromMock(t)

	var got map[string]any
	mock.Handle("thread/start", func(params json.RawMessage) (any, error) {
		if err := json.Unmarshal(params, &got); err != nil {
			return nil, err
		}
		return map[string]any{"thread": map[string]any{"id": "t1"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := client.StartThread(ctx,
		codexgo.WithThreadModel("gpt-5"),
		codexgo.WithThreadModelProvider("acme"),
		codexgo.WithThreadConfigOverride("model_providers.acme", map[string]any{
			"name":                      "acme",
			"base_url":                  "https://acme.example/v1",
			"wire_api":                  "responses",
			"experimental_bearer_token": "sk-tenant",
		}),
	)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	if got["modelProvider"] != "acme" {
		t.Fatalf("modelProvider = %v, want %q", got["modelProvider"], "acme")
	}
	cfg, ok := got["config"].(map[string]any)
	if !ok {
		t.Fatalf("thread/start carried no config overlay: %v", got["config"])
	}
	// The whole provider table rides under the dotted key, exactly as upstream's own
	// thread/start tests send it.
	acme, ok := cfg["model_providers.acme"].(map[string]any)
	if !ok {
		t.Fatalf("config.model_providers.acme missing: %v", cfg)
	}
	if acme["wire_api"] != "responses" {
		t.Fatalf("wire_api = %v, want %q (the only value upstream supports)", acme["wire_api"], "responses")
	}
	if acme["base_url"] != "https://acme.example/v1" {
		t.Fatalf("base_url = %v", acme["base_url"])
	}
	if acme["experimental_bearer_token"] != "sk-tenant" {
		t.Fatalf("bearer token did not reach the wire: %v", acme["experimental_bearer_token"])
	}
}

// Dotted keys are passed through verbatim; the app-server resolves the path (the same
// mechanism as CLI `-c a.b.c=v`). So several overrides can build one provider table.
func TestThreadConfigOverrideKeepsDottedKeys(t *testing.T) {
	client, mock := newClientFromMock(t)

	var got map[string]any
	mock.Handle("thread/start", func(params json.RawMessage) (any, error) {
		if err := json.Unmarshal(params, &got); err != nil {
			return nil, err
		}
		return map[string]any{"thread": map[string]any{"id": "t1"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	thread, err := client.StartThread(ctx,
		codexgo.WithThreadConfigOverride("model_providers.acme.base_url", "https://acme.example/v1"),
		codexgo.WithThreadConfigOverride("model_providers.acme.wire_api", "responses"),
	)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	cfg, _ := got["config"].(map[string]any)
	if cfg["model_providers.acme.base_url"] != "https://acme.example/v1" ||
		cfg["model_providers.acme.wire_api"] != "responses" {
		t.Fatalf("dotted overrides did not reach the wire: %v", cfg)
	}
}
