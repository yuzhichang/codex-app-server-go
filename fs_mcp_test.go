package codexgo_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
	"github.com/zealbase/codex-app-server-go/internal/testutil"
)

// recorded returns a handler that records the params it received and replies with reply.
func recorder(hit *json.RawMessage, reply any) func(json.RawMessage) (any, error) {
	return func(params json.RawMessage) (any, error) {
		if hit != nil {
			*hit = params
		}
		return reply, nil
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The fs/* RPCs drive the app-server's host filesystem. Each call must reach the exact
// upstream wire method and round-trip its typed params/response.
func TestFsRPCsUseUpstreamWireMethods(t *testing.T) {
	cases := []struct {
		name       string
		wireMethod string
		call       func(context.Context, *codexgo.Client) error
	}{
		{"readFile", "fs/readFile", func(ctx context.Context, c *codexgo.Client) error {
			resp, err := c.FSReadFile(ctx, codexgo.FsReadFileParams{Path: "/tmp/a.txt"})
			if err == nil && resp.DataBase64 == "" {
				t.Error("readFile: empty dataBase64")
			}
			return err
		}},
		{"writeFile", "fs/writeFile", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.FSWriteFile(ctx, codexgo.FsWriteFileParams{Path: "/tmp/a.txt", DataBase64: "aGk="})
			return err
		}},
		{"createDirectory", "fs/createDirectory", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.FSCreateDirectory(ctx, codexgo.FsCreateDirectoryParams{Path: "/tmp/d", Recursive: true})
			return err
		}},
		{"getMetadata", "fs/getMetadata", func(ctx context.Context, c *codexgo.Client) error {
			resp, err := c.FSGetMetadata(ctx, codexgo.FsGetMetadataParams{Path: "/tmp/a.txt"})
			if err == nil && !resp.IsFile {
				t.Error("getMetadata: expected isFile true")
			}
			return err
		}},
		{"readDirectory", "fs/readDirectory", func(ctx context.Context, c *codexgo.Client) error {
			resp, err := c.FSReadDirectory(ctx, codexgo.FsReadDirectoryParams{Path: "/tmp"})
			if err == nil && (len(resp.Entries) != 1 || resp.Entries[0].FileName != "a.txt") {
				t.Errorf("readDirectory: unexpected entries %+v", resp.Entries)
			}
			return err
		}},
		{"remove", "fs/remove", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.FSRemove(ctx, codexgo.FsRemoveParams{Path: "/tmp/a.txt", Force: true})
			return err
		}},
		{"copy", "fs/copy", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.FSCopy(ctx, codexgo.FsCopyParams{SourcePath: "/a", DestinationPath: "/b", Recursive: true})
			return err
		}},
		{"watch", "fs/watch", func(ctx context.Context, c *codexgo.Client) error {
			resp, err := c.FSWatch(ctx, codexgo.FsWatchParams{Path: "/tmp", WatchID: "w1"})
			if err == nil && resp.Path != "/tmp" {
				t.Errorf("watch: unexpected path %q", resp.Path)
			}
			return err
		}},
		{"unwatch", "fs/unwatch", func(ctx context.Context, c *codexgo.Client) error {
			_, err := c.FSUnwatch(ctx, codexgo.FsUnwatchParams{WatchID: "w1"})
			return err
		}},
	}

	replies := map[string]any{
		"fs/readFile":        map[string]any{"dataBase64": "aGVsbG8="},
		"fs/writeFile":       map[string]any{},
		"fs/createDirectory": map[string]any{},
		"fs/getMetadata":     map[string]any{"isFile": true, "isDirectory": false, "isSymlink": false},
		"fs/readDirectory":   map[string]any{"entries": []any{map[string]any{"fileName": "a.txt", "isFile": true}}},
		"fs/remove":          map[string]any{},
		"fs/copy":            map[string]any{},
		"fs/watch":           map[string]any{"path": "/tmp"},
		"fs/unwatch":         map[string]any{},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, mock := newClientFromMock(t)
			defer client.Close()

			var got json.RawMessage
			mock.Handle(tc.wireMethod, recorder(&got, replies[tc.wireMethod]))

			if err := tc.call(testCtx(t), client); err != nil {
				t.Fatalf("%s: %v", tc.wireMethod, err)
			}
			if len(got) == 0 {
				t.Fatalf("%s: handler never received params", tc.wireMethod)
			}
		})
	}
}

// fs/readFile and fs/writeFile speak base64 on the wire; the byte helpers must round-trip.
func TestFsByteHelpersRoundTripBase64(t *testing.T) {
	client, mock := newClientFromMock(t)
	defer client.Close()

	payload := []byte("hello\x00world")

	var wrote struct {
		Path       string `json:"path"`
		DataBase64 string `json:"dataBase64"`
	}
	mock.Handle("fs/writeFile", func(params json.RawMessage) (any, error) {
		testutil.MustReadParams(params, &wrote)
		return map[string]any{}, nil
	})
	mock.Handle("fs/readFile", func(json.RawMessage) (any, error) {
		return map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(payload)}, nil
	})

	ctx := testCtx(t)

	if err := client.FSWriteFileBytes(ctx, "/tmp/bin", payload); err != nil {
		t.Fatalf("FSWriteFileBytes: %v", err)
	}
	if wrote.Path != "/tmp/bin" {
		t.Errorf("path = %q, want /tmp/bin", wrote.Path)
	}
	if decoded, err := base64.StdEncoding.DecodeString(wrote.DataBase64); err != nil || string(decoded) != string(payload) {
		t.Errorf("wire payload did not encode the bytes: %q (err=%v)", wrote.DataBase64, err)
	}

	got, err := client.FSReadFileBytes(ctx, "/tmp/bin")
	if err != nil {
		t.Fatalf("FSReadFileBytes: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("round trip mismatch: %q != %q", got, payload)
	}
	// The base64 must not leak into the decoded form.
	if string(got) == base64.StdEncoding.EncodeToString(payload) {
		t.Error("FSReadFileBytes returned the encoded form")
	}
}

func TestMcpRPCsUseUpstreamWireMethods(t *testing.T) {
	client, mock := newClientFromMock(t)
	defer client.Close()

	mock.Handle("mcpServer/oauth/login", func(params json.RawMessage) (any, error) {
		var req codexgo.McpServerOauthLoginParams
		testutil.MustReadParams(params, &req)
		if req.Name != "github" {
			t.Errorf("oauth/login name = %q, want github", req.Name)
		}
		return map[string]any{"authorizationUrl": "https://example.test/auth", "loginId": "l1"}, nil
	})
	mock.Handle("mcpServerStatus/list", func(params json.RawMessage) (any, error) {
		var req codexgo.ListMcpServerStatusParams
		testutil.MustReadParams(params, &req)
		if req.Limit != 10 {
			t.Errorf("status/list limit = %d, want 10", req.Limit)
		}
		return map[string]any{
			"data":       []any{map[string]any{"name": "github", "authStatus": "oAuth"}},
			"nextCursor": "c2",
		}, nil
	})
	mock.Handle("mcpServer/resource/read", func(params json.RawMessage) (any, error) {
		var req codexgo.McpResourceReadParams
		testutil.MustReadParams(params, &req)
		if req.Server != "github" || req.URI != "repo://x" {
			t.Errorf("resource/read params = %+v", req)
		}
		return map[string]any{"contents": []any{map[string]any{"uri": "repo://x"}}}, nil
	})
	mock.Handle("mcpServer/tool/call", func(params json.RawMessage) (any, error) {
		var req codexgo.McpServerToolCallParams
		testutil.MustReadParams(params, &req)
		if req.Server != "github" || req.Tool != "search" || req.ThreadID != "thr_1" {
			t.Errorf("tool/call params = %+v", req)
		}
		// An MCP tool failure is a successful RPC with isError set.
		return map[string]any{"isError": true, "content": []any{}}, nil
	})
	mock.Handle("config/mcpServer/reload", func(json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})

	ctx := testCtx(t)

	login, err := client.MCPServerOauthLogin(ctx, codexgo.McpServerOauthLoginParams{Name: "github"})
	if err != nil {
		t.Fatalf("MCPServerOauthLogin: %v", err)
	}
	if login.AuthorizationURL != "https://example.test/auth" || login.LoginID != "l1" {
		t.Errorf("oauth login response = %+v", login)
	}

	status, err := client.MCPServerStatusList(ctx, codexgo.ListMcpServerStatusParams{Limit: 10})
	if err != nil {
		t.Fatalf("MCPServerStatusList: %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].Name != "github" || status.NextCursor != "c2" {
		t.Errorf("status list response = %+v", status)
	}
	if status.Data[0].AuthStatus != codexgo.McpAuthStatusOAuth {
		t.Errorf("authStatus = %q, want oAuth", status.Data[0].AuthStatus)
	}

	if _, err := client.MCPServerResourceRead(ctx, codexgo.McpResourceReadParams{Server: "github", URI: "repo://x"}); err != nil {
		t.Fatalf("MCPServerResourceRead: %v", err)
	}

	call, err := client.MCPServerToolCall(ctx, codexgo.McpServerToolCallParams{
		Server: "github", Tool: "search", ThreadID: "thr_1",
	})
	if err != nil {
		t.Fatalf("MCPServerToolCall: %v", err)
	}
	if !call.IsError {
		t.Error("expected IsError to be surfaced from the response, not as an RPC error")
	}

	if _, err := client.ConfigMCPServerReload(ctx); err != nil {
		t.Fatalf("ConfigMCPServerReload: %v", err)
	}
}
