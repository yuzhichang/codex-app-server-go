// fs-and-mcp shows the filesystem and MCP lifecycle RPCs.
//
// Two things worth noting:
//
//   - The fs/* RPCs act on the *host* the app-server runs on, not the machine calling this
//     code. Over a remote WebSocket those are different machines.
//   - fs/watch returns a watchId that upstream documents as connection-scoped: a watch does
//     not survive a reconnect. If you are driving a long-lived session, re-establish your
//     watches after recovering (see the reconnect-supervisor example).
//
// Run: CODEX_WS_URL=ws://127.0.0.1:1455 go run ./examples/fs-and-mcp
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

func main() {
	url := os.Getenv("CODEX_WS_URL")
	if url == "" {
		log.Fatal("set CODEX_WS_URL to a codex app-server WebSocket endpoint, e.g. ws://127.0.0.1:1455")
	}
	// The directory to inspect and the MCP server to report on.
	dir := envOr("DEMO_DIR", "/tmp")
	server := envOr("DEMO_MCP_SERVER", "github")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	client, err := codexgo.New(
		codexgo.WithReconnectingWSTransport(ctx, url),
		codexgo.WithAutoReconnect(),
	)
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer client.Close()

	listDirectory(ctx, client, dir)
	readSmallFile(ctx, client, dir+"/hosts")
	listMcpServers(ctx, client)
	readMcpResource(ctx, client, server, "repo://readme")
}

func listDirectory(ctx context.Context, client *codexgo.Client, dir string) {
	// Paths are AbsolutePathBuf: upstream requires an absolute path, and the type says so.
	resp, err := client.FSReadDirectory(ctx, codexgo.FsReadDirectoryParams{
		Path: codexgo.AbsolutePathBuf(dir),
	})
	if err != nil {
		log.Printf("read directory %s: %v", dir, err)
		return
	}
	log.Printf("%s: %d entries", dir, len(resp.Entries))
	for i, e := range resp.Entries {
		if i == 10 {
			break
		}
		kind := "file"
		if e.IsDirectory {
			kind = "dir "
		}
		log.Printf("  %s %s", kind, e.FileName)
	}
}

func readSmallFile(ctx context.Context, client *codexgo.Client, path string) {
	// FSReadFileBytes decodes the base64 the wire carries; the raw RPC returns
	// FsReadFileResponse.DataBase64 if you would rather decode it yourself.
	data, err := client.FSReadFileBytes(ctx, path)
	if err != nil {
		log.Printf("read file %s: %v", path, err)
		return
	}
	preview := string(data)
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	log.Printf("%s (%d bytes): %q", path, len(data), preview)
}

func listMcpServers(ctx context.Context, client *codexgo.Client) {
	// The RPC is mcpServerStatus/list, hence the method name; it is cursor-paginated.
	detail := codexgo.McpServerStatusDetailToolsAndAuthOnly
	resp, err := client.MCPServerStatusList(ctx, codexgo.ListMcpServerStatusParams{
		Limit:  10,
		Detail: &detail,
	})
	if err != nil {
		log.Printf("list mcp servers: %v", err)
		return
	}
	for _, s := range resp.Data {
		log.Printf("mcp %s: auth=%s tools=%d resources=%d",
			s.Name, s.AuthStatus, len(s.Tools), len(s.Resources))
	}
	if resp.NextCursor != "" {
		log.Printf("(more pages available; pass NextCursor=%q)", resp.NextCursor)
	}
}

func readMcpResource(ctx context.Context, client *codexgo.Client, server, uri string) {
	resp, err := client.MCPServerResourceRead(ctx, codexgo.McpResourceReadParams{
		Server: server,
		URI:    uri,
	})
	if err != nil {
		log.Printf("read mcp resource %s: %v", uri, err)
		return
	}
	log.Printf("%s returned %d content block(s)", uri, len(resp.Contents))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
