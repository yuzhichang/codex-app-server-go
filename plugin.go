package codexgo

import (
	"context"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

// Plugin and marketplace RPCs.
//
// A "marketplace" is a source of plugins (a git repo or similar); plugins are installed
// from it and can be shared to remote targets.
//
// Concurrency note: upstream declares several of these with
// `serialization: global("config")` -- plugin/install, plugin/uninstall, marketplace/* and
// plugin/share/*. The server serializes them against the whole config, so concurrent calls
// queue instead of interleaving. That is a server-side guarantee, not something the SDK
// enforces; do not rely on two config mutations applying in the order you issued them.
//
// `plugin/search` is experimental upstream and is deliberately not implemented (decision R2).

// --- marketplace ---

// MarketplaceAdd registers a new marketplace source.
func (c *Client) MarketplaceAdd(ctx context.Context, req MarketplaceAddParams) (MarketplaceAddResponse, error) {
	var resp MarketplaceAddResponse
	if err := c.transport.Call(ctx, protocol.MethodMarketplaceAdd, req, &resp); err != nil {
		return MarketplaceAddResponse{}, err
	}
	return resp, nil
}

// MarketplaceRemove deregisters a marketplace source.
func (c *Client) MarketplaceRemove(ctx context.Context, req MarketplaceRemoveParams) (MarketplaceRemoveResponse, error) {
	var resp MarketplaceRemoveResponse
	if err := c.transport.Call(ctx, protocol.MethodMarketplaceRemove, req, &resp); err != nil {
		return MarketplaceRemoveResponse{}, err
	}
	return resp, nil
}

// MarketplaceUpgrade refreshes one or all marketplaces from their sources.
func (c *Client) MarketplaceUpgrade(ctx context.Context, req MarketplaceUpgradeParams) (MarketplaceUpgradeResponse, error) {
	var resp MarketplaceUpgradeResponse
	if err := c.transport.Call(ctx, protocol.MethodMarketplaceUpgrade, req, &resp); err != nil {
		return MarketplaceUpgradeResponse{}, err
	}
	return resp, nil
}

// --- plugin discovery and reads ---

// PluginList lists plugins available across the configured marketplaces. Cursor-paginated.
func (c *Client) PluginList(ctx context.Context, req PluginListParams) (PluginListResponse, error) {
	var resp PluginListResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginList, req, &resp); err != nil {
		return PluginListResponse{}, err
	}
	return resp, nil
}

// PluginInstalled lists plugins currently installed locally.
func (c *Client) PluginInstalled(ctx context.Context, req PluginInstalledParams) (PluginInstalledResponse, error) {
	var resp PluginInstalledResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginInstalled, req, &resp); err != nil {
		return PluginInstalledResponse{}, err
	}
	return resp, nil
}

// PluginReconcile reconciles installed plugins against what the marketplaces declare,
// reporting what changed. Read-only: it reports drift rather than applying it.
func (c *Client) PluginReconcile(ctx context.Context, req PluginReconcileParams) (PluginReconcileResponse, error) {
	var resp PluginReconcileResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginReconcile, req, &resp); err != nil {
		return PluginReconcileResponse{}, err
	}
	return resp, nil
}

// PluginRead returns the full detail for a single plugin (its manifest, hooks, interfaces).
func (c *Client) PluginRead(ctx context.Context, req PluginReadParams) (PluginReadResponse, error) {
	var resp PluginReadResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginRead, req, &resp); err != nil {
		return PluginReadResponse{}, err
	}
	return resp, nil
}

// PluginSkillRead returns the raw contents of one skill inside a remote plugin. The
// contents may be absent (nil) when the skill has no inline body.
func (c *Client) PluginSkillRead(ctx context.Context, req PluginSkillReadParams) (PluginSkillReadResponse, error) {
	var resp PluginSkillReadResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginSkillRead, req, &resp); err != nil {
		return PluginSkillReadResponse{}, err
	}
	return resp, nil
}

// --- plugin sharing ---

// PluginShareSave publishes a local plugin to the sharing backend.
func (c *Client) PluginShareSave(ctx context.Context, req PluginShareSaveParams) (PluginShareSaveResponse, error) {
	var resp PluginShareSaveResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginShareSave, req, &resp); err != nil {
		return PluginShareSaveResponse{}, err
	}
	return resp, nil
}

// PluginShareUpdateTargets changes who a shared plugin is shared with.
func (c *Client) PluginShareUpdateTargets(ctx context.Context, req PluginShareUpdateTargetsParams) (PluginShareUpdateTargetsResponse, error) {
	var resp PluginShareUpdateTargetsResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginShareUpdateTargets, req, &resp); err != nil {
		return PluginShareUpdateTargetsResponse{}, err
	}
	return resp, nil
}

// PluginShareList lists plugins this user has shared. Cursor-paginated.
func (c *Client) PluginShareList(ctx context.Context, req PluginShareListParams) (PluginShareListResponse, error) {
	var resp PluginShareListResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginShareList, req, &resp); err != nil {
		return PluginShareListResponse{}, err
	}
	return resp, nil
}

// PluginShareCheckout materializes a shared plugin locally.
func (c *Client) PluginShareCheckout(ctx context.Context, req PluginShareCheckoutParams) (PluginShareCheckoutResponse, error) {
	var resp PluginShareCheckoutResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginShareCheckout, req, &resp); err != nil {
		return PluginShareCheckoutResponse{}, err
	}
	return resp, nil
}

// PluginShareDelete unshares a plugin.
func (c *Client) PluginShareDelete(ctx context.Context, req PluginShareDeleteParams) (PluginShareDeleteResponse, error) {
	var resp PluginShareDeleteResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginShareDelete, req, &resp); err != nil {
		return PluginShareDeleteResponse{}, err
	}
	return resp, nil
}

// --- install / uninstall ---

// PluginInstall installs a plugin from a marketplace. InstallAttemptId is echoed back on
// failures so a retried attempt can be correlated.
func (c *Client) PluginInstall(ctx context.Context, req PluginInstallParams) (PluginInstallResponse, error) {
	var resp PluginInstallResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginInstall, req, &resp); err != nil {
		return PluginInstallResponse{}, err
	}
	return resp, nil
}

// PluginUninstall removes an installed plugin.
func (c *Client) PluginUninstall(ctx context.Context, req PluginUninstallParams) (PluginUninstallResponse, error) {
	var resp PluginUninstallResponse
	if err := c.transport.Call(ctx, protocol.MethodPluginUninstall, req, &resp); err != nil {
		return PluginUninstallResponse{}, err
	}
	return resp, nil
}
