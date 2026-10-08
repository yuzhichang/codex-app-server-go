package codexgo

import (
	"context"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

// MCP lifecycle RPCs (mcpServer/*, config/mcpServer/*).
//
// These manage and drive the MCP servers the app-server knows about. The client-side
// elicitation handler lives in interaction.go (Dispatcher.MCP): when an MCP server asks the
// user for input, the server sends `mcpServer/elicitation/request` and the installed
// handler must answer with an action (accept/decline/cancel).

// MCPServerOauthLogin begins an OAuth login for a named MCP server and returns the URL the
// user must visit. Completion arrives asynchronously as McpServerOauthLoginCompletedEvent.
func (c *Client) MCPServerOauthLogin(ctx context.Context, req McpServerOauthLoginParams) (McpServerOauthLoginResponse, error) {
	var resp McpServerOauthLoginResponse
	if err := c.transport.Call(ctx, protocol.MethodMcpServerOauthLogin, req, &resp); err != nil {
		return McpServerOauthLoginResponse{}, err
	}
	return resp, nil
}

// MCPServerStatusList lists known MCP servers with their auth/runtime status. It is
// cursor-paginated: pass the previous response's NextCursor to fetch the next page.
//
// Detail selects how much per-server detail to include (full, or toolsAndAuthOnly).
func (c *Client) MCPServerStatusList(ctx context.Context, req ListMcpServerStatusParams) (ListMcpServerStatusResponse, error) {
	var resp ListMcpServerStatusResponse
	if err := c.transport.Call(ctx, protocol.MethodMcpServerStatusList, req, &resp); err != nil {
		return ListMcpServerStatusResponse{}, err
	}
	return resp, nil
}

// MCPServerResourceRead reads a resource exposed by an MCP server.
func (c *Client) MCPServerResourceRead(ctx context.Context, req McpResourceReadParams) (McpResourceReadResponse, error) {
	var resp McpResourceReadResponse
	if err := c.transport.Call(ctx, protocol.MethodMcpServerResourceRead, req, &resp); err != nil {
		return McpResourceReadResponse{}, err
	}
	return resp, nil
}

// MCPServerToolCall invokes a tool on an MCP server.
//
// Note that a failure is reported inside the response (IsError), not necessarily as a
// transport error: an MCP tool returning an error is a successful RPC.
func (c *Client) MCPServerToolCall(ctx context.Context, req McpServerToolCallParams) (McpServerToolCallResponse, error) {
	var resp McpServerToolCallResponse
	if err := c.transport.Call(ctx, protocol.MethodMcpServerToolCall, req, &resp); err != nil {
		return McpServerToolCallResponse{}, err
	}
	return resp, nil
}

// ConfigMCPServerReload re-reads MCP server configuration and restarts changed servers.
// Upstream serializes this globally (config-level mutation), so concurrent calls queue.
func (c *Client) ConfigMCPServerReload(ctx context.Context) (McpServerRefreshResponse, error) {
	var resp McpServerRefreshResponse
	if err := c.transport.Call(ctx, protocol.MethodConfigMcpServerReload, nil, &resp); err != nil {
		return McpServerRefreshResponse{}, err
	}
	return resp, nil
}
