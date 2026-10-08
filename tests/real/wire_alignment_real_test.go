package real_test

import (
	"context"
	"os"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// D1 verification against a real server (required by the plan's T1.4: the jsonrpc removal
// must be validated against a live app-server, not only against unit tests).
//
// Upstream does not use true JSON-RPC 2.0:
//
//	//! We do not do true JSON-RPC 2.0, as we neither send nor expect the
//	//! "jsonrpc": "2.0" field.        -- app-server-protocol/src/rpc.rs:1-2
//
// codexgo.New with a WebSocket transport performs the `initialize` + `initialized`
// handshake itself, so a successful construction already proves the server accepts a
// handshake carrying no `jsonrpc` field. We then issue read-only RPCs that require no
// model credentials, so this test stays cheap and hermetic.
//
// Run against a local server:
//
//	codex app-server --listen ws://127.0.0.1:8791 &
//	CODEX_REAL_ENDPOINT=ws://127.0.0.1:8791 go test ./tests/real/ -run WSHandshake -v
func TestReal_WSHandshakeWithoutJSONRPCField(t *testing.T) {
	skipIfNoEndpoint(t)
	endpoint := os.Getenv("CODEX_REAL_ENDPOINT")

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelDial()

	client, err := codexgo.New(
		codexgo.WithWSTransport(dialCtx, endpoint),
		codexgo.WithClientInfo("codex-go-sdk-wire-test", "Wire alignment test", "0.0.0"),
	)
	if err != nil {
		t.Fatalf("New (field-less WS handshake) failed: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := client.Models(ctx); err != nil {
		t.Fatalf("model/list over a field-less envelope failed: %v", err)
	}
	if _, err := client.ThreadLoadedList(ctx); err != nil {
		t.Fatalf("thread/loaded/list over a field-less envelope failed: %v", err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping (thread/list) over a field-less envelope failed: %v", err)
	}
}
