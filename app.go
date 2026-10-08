package codexgo

import (
	"context"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

// App registry RPCs (app/*).
//
// "Apps" are the declared applications the app-server knows about (their config, branding,
// tools and requirements). Reads are cursor-paginated; catalog changes arrive
// asynchronously as AppListUpdatedEvent.
//
// Upstream prefixes these types with "Apps" (AppsListParams, not AppListParams).

// AppsList lists available apps. Cursor-paginated: pass the previous response's cursor.
// Set ForceRefetch to bypass any cached catalog.
func (c *Client) AppsList(ctx context.Context, req AppsListParams) (AppsListResponse, error) {
	var resp AppsListResponse
	if err := c.transport.Call(ctx, protocol.MethodAppsList, req, &resp); err != nil {
		return AppsListResponse{}, err
	}
	return resp, nil
}

// AppsInstalled lists the apps currently installed for this user.
func (c *Client) AppsInstalled(ctx context.Context, req AppsInstalledParams) (AppsInstalledResponse, error) {
	var resp AppsInstalledResponse
	if err := c.transport.Call(ctx, protocol.MethodAppsInstalled, req, &resp); err != nil {
		return AppsInstalledResponse{}, err
	}
	return resp, nil
}

// AppsRead returns the full definition of a single app.
func (c *Client) AppsRead(ctx context.Context, req AppsReadParams) (AppsReadResponse, error) {
	var resp AppsReadResponse
	if err := c.transport.Call(ctx, protocol.MethodAppsRead, req, &resp); err != nil {
		return AppsReadResponse{}, err
	}
	return resp, nil
}
