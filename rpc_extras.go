package codexgo

import (
	"context"
	"encoding/json"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
)

// Config RPCs.
//
// These replace the removed `config/update` (see gen/unknown-methods.txt and plan T1.5):
// upstream now exposes granular, per-key writes with optional optimistic concurrency via
// `expectedVersion`, rather than a single blob-shaped update.
//
// The request/response types are the schema-derived ones and keep their upstream names
// (`*Params`/`Response`); they are aliased in types.go. The SDK's former hand-written
// ConfigReadRequest/ConfigReadResult/ConfigValueWriteRequest/ConfigBatchWriteRequest
// duplicates were removed rather than kept alongside them.

// ConfigRead returns the effective configuration.
//
// Set IncludeLayers to also get the per-layer breakdown. Origins maps a key path to the
// layer that supplied it together with that layer's version -- the version is what
// ConfigValueWrite needs as ExpectedVersion for an unconditional read-modify-write.
func (c *Client) ConfigRead(ctx context.Context, req ConfigReadParams) (ConfigReadResponse, error) {
	var resp ConfigReadResponse
	if err := c.transport.Call(ctx, protocol.MethodConfigRead, req, &resp); err != nil {
		return ConfigReadResponse{}, err
	}
	return resp, nil
}

// ConfigValueWrite writes a single configuration value at the given key path.
//
// The response reports which file was written and whether the write was overridden by a
// higher-precedence layer -- check Status rather than assuming success.
func (c *Client) ConfigValueWrite(ctx context.Context, req ConfigValueWriteParams) (ConfigWriteResponse, error) {
	var resp ConfigWriteResponse
	if err := c.transport.Call(ctx, protocol.MethodConfigValueWrite, req, &resp); err != nil {
		return ConfigWriteResponse{}, err
	}
	return resp, nil
}

// ConfigBatchWrite applies a batch of configuration edits atomically.
func (c *Client) ConfigBatchWrite(ctx context.Context, req ConfigBatchWriteParams) (ConfigWriteResponse, error) {
	var resp ConfigWriteResponse
	if err := c.transport.Call(ctx, protocol.MethodConfigBatchWrite, req, &resp); err != nil {
		return ConfigWriteResponse{}, err
	}
	return resp, nil
}

type (
	// SkillsListEntry is the per-cwd skills listing.
	SkillsListEntry = schematypes.SkillsListEntry
	// SkillMetadata describes a single discovered skill.
	SkillMetadata = schematypes.SkillMetadata
	// SkillErrorInfo describes a skill discovery error.
	SkillErrorInfo = schematypes.SkillErrorInfo

	SkillsListRequest          = schematypes.SkillsListParams
	SkillsListResult           = schematypes.SkillsListResponse
	SkillsConfigWriteRequest   = schematypes.SkillsConfigWriteParams
	SkillsConfigWriteResult    = schematypes.SkillsConfigWriteResponse
	SkillsExtraRootsSetRequest = schematypes.SkillsExtraRootsSetParams
)

// SkillsList lists skills discoverable from the given working directories.
func (c *Client) SkillsList(ctx context.Context, req SkillsListRequest) (SkillsListResult, error) {
	var resp SkillsListResult
	if err := c.transport.Call(ctx, protocol.MethodSkillsList, req, &resp); err != nil {
		return SkillsListResult{}, err
	}
	return resp, nil
}

// SkillsConfigWrite enables or disables a skill (by name or path) and returns
// the effective enabled state.
func (c *Client) SkillsConfigWrite(ctx context.Context, req SkillsConfigWriteRequest) (SkillsConfigWriteResult, error) {
	var resp SkillsConfigWriteResult
	if err := c.transport.Call(ctx, protocol.MethodSkillsConfigWrite, req, &resp); err != nil {
		return SkillsConfigWriteResult{}, err
	}
	return resp, nil
}

// SkillsExtraRootsSet sets additional filesystem roots scanned for skills.
func (c *Client) SkillsExtraRootsSet(ctx context.Context, req SkillsExtraRootsSetRequest) error {
	return c.transport.Call(ctx, protocol.MethodSkillsExtraRootsSet, req, nil)
}

// ExperimentalFeature describes a single experimental feature flag.
type ExperimentalFeature struct {
	Name           string          `json:"name"`
	DisplayName    string          `json:"displayName,omitempty"`
	Description    string          `json:"description,omitempty"`
	Announcement   string          `json:"announcement,omitempty"`
	Enabled        bool            `json:"enabled"`
	DefaultEnabled bool            `json:"defaultEnabled"`
	Stage          json.RawMessage `json:"stage,omitempty"`
}

// ExperimentalFeatureListRequest lists experimental features. Cursor/Limit drive
// pagination; an empty NextCursor in the result indicates the final page.
type ExperimentalFeatureListRequest struct {
	ThreadID string `json:"threadId,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ExperimentalFeatureListResult is a page of experimental features.
type ExperimentalFeatureListResult struct {
	Data       []ExperimentalFeature `json:"data"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

// ExperimentalFeatureEnablementSetRequest sets enablement for one or more
// features, keyed by feature name.
type ExperimentalFeatureEnablementSetRequest struct {
	Enablement map[string]bool `json:"enablement"`
}

// ExperimentalFeatureEnablementSetResult reports the resulting enablement map.
type ExperimentalFeatureEnablementSetResult struct {
	Enablement map[string]bool `json:"enablement"`
}

// ExperimentalFeatureList returns a page of experimental features.
func (c *Client) ExperimentalFeatureList(ctx context.Context, req ExperimentalFeatureListRequest) (ExperimentalFeatureListResult, error) {
	var resp ExperimentalFeatureListResult
	if err := c.transport.Call(ctx, protocol.MethodExperimentalFeatureList, req, &resp); err != nil {
		return ExperimentalFeatureListResult{}, err
	}
	return resp, nil
}

// ExperimentalFeatureEnablementSet enables or disables experimental features.
func (c *Client) ExperimentalFeatureEnablementSet(ctx context.Context, req ExperimentalFeatureEnablementSetRequest) (ExperimentalFeatureEnablementSetResult, error) {
	var resp ExperimentalFeatureEnablementSetResult
	if err := c.transport.Call(ctx, protocol.MethodExperimentalFeatureEnablementSet, req, &resp); err != nil {
		return ExperimentalFeatureEnablementSetResult{}, err
	}
	return resp, nil
}

// HooksListRequest lists configured lifecycle hooks for the given working
// directories. This is the server-side hooks/list RPC and is distinct from the
// client-side hook bridge (internal/hookbridge).
type HooksListRequest struct {
	CWDs []string `json:"cwds,omitempty"`
}

// HooksListResult holds the hooks listing. Data is passed through as raw JSON
// (an array of per-cwd hook entries) for forward compatibility.
type HooksListResult struct {
	Data json.RawMessage `json:"data"`
}

// HooksList returns the configured lifecycle hooks discoverable from the given
// working directories.
func (c *Client) HooksList(ctx context.Context, req HooksListRequest) (HooksListResult, error) {
	var resp HooksListResult
	if err := c.transport.Call(ctx, protocol.MethodHooksList, req, &resp); err != nil {
		return HooksListResult{}, err
	}
	return resp, nil
}

// ModelProviderCapabilities reports which provider-backed capabilities are
// available for the current session.
type ModelProviderCapabilities struct {
	NamespaceTools  bool `json:"namespaceTools"`
	ImageGeneration bool `json:"imageGeneration"`
	WebSearch       bool `json:"webSearch"`
}

// ModelProviderCapabilitiesRead returns the capabilities the active model
// provider supports (namespaced tools, image generation, web search).
func (c *Client) ModelProviderCapabilitiesRead(ctx context.Context) (ModelProviderCapabilities, error) {
	var resp ModelProviderCapabilities
	if err := c.transport.Call(ctx, protocol.MethodModelProviderCapabilitiesRead, struct{}{}, &resp); err != nil {
		return ModelProviderCapabilities{}, err
	}
	return resp, nil
}
