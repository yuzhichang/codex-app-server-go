package codexgo

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
)

// threadIDTransport answers thread/start and thread/fork with a fixed thread id and nothing
// else, so the supervision bookkeeping can be observed without a server.
type threadIDTransport struct{}

func (threadIDTransport) Call(_ context.Context, method string, _ any, result any) error {
	var id string
	switch method {
	case protocol.MethodThreadStart:
		id = "thr-start"
	case protocol.MethodThreadFork:
		id = "thr-fork"
	default:
		return nil
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal([]byte(`{"thread":{"id":"`+id+`"}}`), result)
}
func (threadIDTransport) Notify(context.Context, string, any) error { return nil }
func (threadIDTransport) SetRequestHandler(RequestHandler)          {}
func (threadIDTransport) Close() error                              { return nil }

// A forked thread is meant to be open on this connection, so it must be resumed and
// backfilled after a reconnect exactly like StartThread/ResumeThread's threads. Fork used to
// skip trackThread, so a recovery resumed only the parent and silently left the fork behind.
func TestForkedThreadsAreTrackedForRecovery(t *testing.T) {
	client, err := New(WithTransport(threadIDTransport{}))
	if err != nil {
		t.Fatal(err)
	}
	client.supervisor = newSessionSupervisor(client)

	parent, err := client.StartThread(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Fork(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	got := client.supervisor.trackedThreads()
	if !slices.Contains(got, "thr-start") || !slices.Contains(got, "thr-fork") {
		t.Fatalf("tracked threads = %v, want the parent and the fork", got)
	}
}

// Backfilled history must carry item bodies, or a turn that actually contains an agent
// message comes back empty and FinalAgentText has nothing to return. The schema ThreadItem
// used to keep only id/type, so the payload was dropped in this conversion.
func TestBackfilledTurnsKeepItemBodies(t *testing.T) {
	in := []schematypes.Turn{{
		ID:     "t1",
		Status: schematypes.TurnStatusCompleted,
		Items:  []schematypes.ThreadItem{{Type: "agentMessage", ID: "i1", Text: "hello"}},
	}}
	out, err := turnsFromSchema(in)
	if err != nil {
		t.Fatalf("turnsFromSchema: %v", err)
	}
	if len(out) != 1 || len(out[0].Items) != 1 {
		t.Fatalf("converted turns = %+v", out)
	}
	if !strings.Contains(string(out[0].Items[0].Payload), "hello") {
		t.Fatalf("item body lost in conversion: payload = %q", out[0].Items[0].Payload)
	}
}

// The session gate must hold normal calls while the session is being re-established, and let
// them through once it is. Without it a call could reach the new socket before the handshake
// was replayed.
func TestSessionGateBlocksCallsUntilReleased(t *testing.T) {
	g := newSessionGate(threadIDTransport{})
	g.arm()

	done := make(chan error, 1)
	go func() { done <- g.Call(context.Background(), "thread/start", nil, nil) }()

	select {
	case <-done:
		t.Fatal("a call proceeded while the session gate was armed")
	case <-time.After(80 * time.Millisecond):
	}

	g.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Call after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a call did not proceed after the gate was released")
	}
}

// A gated call still honours its own context deadline rather than waiting forever.
func TestSessionGateHonoursContext(t *testing.T) {
	g := newSessionGate(threadIDTransport{})
	g.arm()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := g.Call(ctx, "thread/start", nil, nil); err == nil {
		t.Fatal("expected a context error while the gate was armed")
	}
}
