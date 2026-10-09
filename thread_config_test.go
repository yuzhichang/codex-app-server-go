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

// The thread/start override set must mirror ThreadResumeParams' for these four fields; setting
// them on Start must reach the wire. In particular `sandbox` is a MODE STRING (upstream
// SandboxMode), not the tagged policy object turn input takes.
func TestThreadStartSendsSandboxAndInstructionOverrides(t *testing.T) {
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
		codexgo.WithThreadSandbox(codexgo.SandboxReadOnly),
		codexgo.WithThreadServiceTier("priority"),
		codexgo.WithThreadBaseInstructions("be terse"),
		codexgo.WithThreadDeveloperInstructions("no tools"),
	)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	sandbox, ok := got["sandbox"].(string)
	if !ok {
		t.Fatalf("sandbox = %#v, want a mode string (not a policy object)", got["sandbox"])
	}
	if sandbox != string(codexgo.SandboxReadOnly) {
		t.Fatalf("sandbox = %q, want %q", sandbox, codexgo.SandboxReadOnly)
	}
	if got["serviceTier"] != "priority" {
		t.Fatalf("serviceTier = %v", got["serviceTier"])
	}
	if got["baseInstructions"] != "be terse" || got["developerInstructions"] != "no tools" {
		t.Fatalf("instruction overrides did not reach the wire: %v", got)
	}
}

// Resume must forward the same override set Start does. Client.ResumeThread used to build
// ThreadResumeParams{ThreadID} only and silently drop every override, so a resumed thread
// depended on whatever the server happened to store at thread/start (and, e.g., a per-thread
// MCP server config could not be refreshed). ThreadResumeParams already carries these fields;
// pin that they reach thread/resume.
func TestResumeThreadForwardsOverrides(t *testing.T) {
	client, mock := newClientFromMock(t)

	var got map[string]any
	mock.Handle("thread/resume", func(params json.RawMessage) (any, error) {
		if err := json.Unmarshal(params, &got); err != nil {
			return nil, err
		}
		return map[string]any{"thread": map[string]any{"id": "t1"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := client.ResumeThread(ctx, "t1",
		codexgo.WithThreadModel("gpt-5.4"),
		codexgo.WithThreadModelProvider("acme"),
		codexgo.WithThreadConfigOverride("mcp_servers.ragflow", map[string]any{
			"url": "http://ragflow.local/mcp/codex/tok",
		}),
		codexgo.WithThreadSandbox(codexgo.SandboxReadOnly),
		codexgo.WithThreadBaseInstructions("cite chunk ids"),
	)
	if err != nil {
		t.Fatalf("ResumeThread: %v", err)
	}
	defer thread.Close()

	if got["model"] != "gpt-5.4" {
		t.Fatalf("model = %v, want gpt-5.4", got["model"])
	}
	if got["modelProvider"] != "acme" {
		t.Fatalf("modelProvider = %v, want acme", got["modelProvider"])
	}
	if got["sandbox"] != string(codexgo.SandboxReadOnly) {
		t.Fatalf("sandbox = %v, want %q", got["sandbox"], codexgo.SandboxReadOnly)
	}
	if got["baseInstructions"] != "cite chunk ids" {
		t.Fatalf("baseInstructions = %v", got["baseInstructions"])
	}
	cfg, ok := got["config"].(map[string]any)
	if !ok {
		t.Fatalf("thread/resume carried no config overlay: %v", got["config"])
	}
	srv, ok := cfg["mcp_servers.ragflow"].(map[string]any)
	if !ok || srv["url"] != "http://ragflow.local/mcp/codex/tok" {
		t.Fatalf("mcp override did not reach thread/resume: %v", cfg)
	}
}
