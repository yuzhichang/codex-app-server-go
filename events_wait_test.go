package codexgo

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	"github.com/zealbase/codex-app-server-go/internal/transport"
)

type fakeNotificationTransport struct {
	fakeTransportWithErrors
	notes chan transport.Notification
	once  sync.Once
}

func newFakeNotificationTransport() *fakeNotificationTransport {
	return &fakeNotificationTransport{
		notes: make(chan transport.Notification, 16),
	}
}

func (f *fakeNotificationTransport) Notifications() <-chan transport.Notification {
	return f.notes
}

func (f *fakeNotificationTransport) Close() error {
	f.once.Do(func() {
		close(f.notes)
	})
	return f.fakeTransportWithErrors.Close()
}

func TestEventsPublishesTypedNotifications(t *testing.T) {
	ft := newFakeNotificationTransport()
	client, err := New(WithTransport(ft))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	sub := client.Events()
	defer sub.Close()

	ft.notes <- transport.Notification{
		Method: protocol.MethodTurnCompleted,
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","status":"completed"}`),
	}

	select {
	case event := <-sub.C():
		if event.Method != protocol.MethodTurnCompleted {
			t.Fatalf("event method = %q", event.Method)
		}
		typed, ok := event.Value.(TurnCompletedEvent)
		if !ok {
			t.Fatalf("event value type = %T", event.Value)
		}
		if typed.ThreadID != "thread-1" || typed.TurnID != "turn-1" || typed.Status != TurnStatusCompleted {
			t.Fatalf("unexpected typed event: %+v", typed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWaitForTurnReturnsTerminalTurn(t *testing.T) {
	ft := newFakeNotificationTransport()
	var completed atomic.Bool
	ft.callResultFunc = func(method string, _ any) any {
		if method != protocol.MethodThreadRead {
			return nil
		}
		status := TurnStatusInProgress
		if completed.Load() {
			status = TurnStatusCompleted
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-1",
				"turns": []any{
					map[string]any{"id": "turn-1", "status": status},
				},
			},
		}
	}

	client, err := New(WithTransport(ft))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		completed.Store(true)
		ft.notes <- transport.Notification{
			Method: protocol.MethodTurnCompleted,
			Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","status":"completed"}`),
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	turn, err := client.WaitForTurn(ctx, "thread-1", "turn-1")
	if err != nil {
		t.Fatalf("WaitForTurn() error = %v", err)
	}
	if turn.ID != "turn-1" || turn.Status != TurnStatusCompleted {
		t.Fatalf("unexpected turn: %+v", turn)
	}
}

func TestWaitForTurnMatchesNestedTurnIDInCompletionEvent(t *testing.T) {
	ft := newFakeNotificationTransport()
	var completed atomic.Bool
	ft.callResultFunc = func(method string, _ any) any {
		if method != protocol.MethodThreadRead {
			return nil
		}
		status := TurnStatusInProgress
		if completed.Load() {
			status = TurnStatusCompleted
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-1",
				"turns": []any{
					map[string]any{"id": "turn-1", "status": status},
				},
			},
		}
	}

	client, err := New(WithTransport(ft))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		completed.Store(true)
		ft.notes <- transport.Notification{
			Method: protocol.MethodTurnCompleted,
			Params: json.RawMessage(`{
				"threadId":"thread-1",
				"turn":{"id":"turn-1","status":"completed","items":[]}
			}`),
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	turn, err := client.WaitForTurn(ctx, "thread-1", "turn-1")
	if err != nil {
		t.Fatalf("WaitForTurn() error = %v", err)
	}
	if turn.ID != "turn-1" || turn.Status != TurnStatusCompleted {
		t.Fatalf("unexpected turn: %+v", turn)
	}
}

func TestWaitForStructuredOutputUsesItemEvents(t *testing.T) {
	ft := newFakeNotificationTransport()
	var completed atomic.Bool
	ft.callResultFunc = func(method string, _ any) any {
		if method != protocol.MethodThreadRead {
			return nil
		}
		status := "inProgress"
		if completed.Load() {
			status = "completed"
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-1",
				"turns": []any{
					map[string]any{
						"id":     "turn-1",
						"status": status,
						"items": []any{
							map[string]any{
								"id":   "item-user",
								"type": "userMessage",
								"text": "prompt",
							},
						},
					},
				},
			},
		}
	}

	client, err := New(WithTransport(ft))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		ft.notes <- transport.Notification{
			Method: protocol.MethodItemCompleted,
			Params: json.RawMessage(`{
				"threadId":"thread-1",
				"turnId":"turn-1",
				"item":{
					"id":"item-agent",
					"type":"agentMessage",
					"text":"{\"message\":\"hello\"}"
				}
			}`),
		}
		completed.Store(true)
		ft.notes <- transport.Notification{
			Method: protocol.MethodTurnCompleted,
			Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","status":"completed"}`),
		}
	}()

	var out struct {
		Message string `json:"message"`
	}
	turn, err := client.WaitForStructuredOutput(ctx, "thread-1", "turn-1", &out)
	if err != nil {
		t.Fatalf("WaitForStructuredOutput() error = %v", err)
	}
	if turn.ID != "turn-1" || out.Message != "hello" {
		t.Fatalf("unexpected result: turn=%+v out=%+v", turn, out)
	}
}

// TestEventMatchesTurnForThreadCompacted pins the compaction notification into the turn
// stream. thread/compacted used to fall through eventMatchesTurn's default (false), so a
// caller streaming a turn never learned that Codex had compacted its context. The match is
// THREAD-level on purpose: compaction can run as its own non-steerable turn, so its turnId
// may differ from the turn being streamed.
func TestEventMatchesTurnForThreadCompacted(t *testing.T) {
	sameThreadDifferentTurn := Event{Value: ThreadCompactedEvent{ThreadID: "thread-1", TurnID: "compact-turn"}}
	if !eventMatchesTurn(sameThreadDifferentTurn, "thread-1", "turn-1") {
		t.Fatal("thread/compacted on the matching thread must match, even with a different turn id")
	}
	otherThread := Event{Value: ThreadCompactedEvent{ThreadID: "thread-2", TurnID: "turn-1"}}
	if eventMatchesTurn(otherThread, "thread-1", "turn-1") {
		t.Fatal("thread/compacted for another thread must not match")
	}
}
