// custom-provider demonstrates pointing one thread at your own model provider at runtime,
// without editing the codex app-server's config.
//
// The provider (base_url + bearer token + wire_api) is passed as a per-thread config overlay
// and selected by id. The app-server deep-merges the overlay into its own config for THIS
// thread only, so a shared app-server can serve many callers that each bring their own
// provider -- the server config is left untouched.
//
// The endpoint MUST speak the Responses API: upstream's WireApi has only the "responses"
// variant, so wire_api is always "responses" (Chat Completions is not supported).
//
//	go run ./examples/custom-provider "Say hello in one sentence"
//
// Environment:
//
//	CODEX_PROVIDER_ID        provider id to declare and select   (default "acme")
//	CODEX_PROVIDER_BASE_URL  e.g. https://acme.internal/v1       (required)
//	CODEX_PROVIDER_API_KEY   bearer token for that endpoint      (required)
//	CODEX_MODEL              model name to request               (default "gpt-5")
//
// Notes:
//   - The key is sent inline as `experimental_bearer_token`. Upstream marks that field as
//     discouraged in favour of `env_key`, but `env_key` names an environment variable of the
//     app-server PROCESS, so it cannot express a per-caller key on a shared server. If your
//     app-server is per-tenant, prefer `env_key` and keep the secret out of the request.
//   - This sets the model endpoint only. Codex also runs commands/sandboxed work in the
//     thread's cwd; isolate that separately (per-thread cwd + sandbox, or a per-tenant
//     process) when callers are mutually untrusted.
package main

import (
	"context"
	"log"
	"os"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

func main() {
	providerID := envOr("CODEX_PROVIDER_ID", "acme")
	baseURL := os.Getenv("CODEX_PROVIDER_BASE_URL")
	apiKey := os.Getenv("CODEX_PROVIDER_API_KEY")
	model := envOr("CODEX_MODEL", "gpt-5")

	if baseURL == "" || apiKey == "" {
		log.Fatal("set CODEX_PROVIDER_BASE_URL and CODEX_PROVIDER_API_KEY (see this file's header)")
	}

	prompt := "Say hello in one sentence."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}

	bin, err := codexgo.FindBinary()
	if err != nil {
		log.Fatalf("codex binary not found: %v (set CODEX_BIN or install codex in PATH)", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := codexgo.New(codexgo.WithStdioProcess(bin, "app-server"))
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer client.Close()

	// Declare the provider in the thread's config overlay and select it by id. Passing the
	// whole provider table as one nested object under "model_providers.<id>" is equivalent to
	// adding "model_providers.<id>.<field>" overrides one by one.
	thread, err := client.StartThread(ctx,
		codexgo.WithThreadModel(model),
		codexgo.WithThreadModelProvider(providerID),
		codexgo.WithThreadConfigOverride("model_providers."+providerID, map[string]any{
			"name":                      providerID,
			"base_url":                  baseURL,
			"wire_api":                  "responses", // the only wire API upstream supports
			"experimental_bearer_token": apiKey,
		}),
		// A plain conversational prompt should not need approvals; keep the turn from stalling
		// on one by declining anything that is asked.
		codexgo.WithThreadApprovalPolicy(codexgo.ApprovalNever()),
	)
	if err != nil {
		log.Fatalf("start thread: %v", err)
	}
	defer thread.Close()

	result, err := thread.Run(ctx, prompt)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	log.Printf("thread %s replied: %s", thread.ID(), result.FinalAgentText())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
