package codexgo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	"github.com/zealbase/codex-app-server-go/internal/transport"
)

// streamRaceTransport emits a turn's server notifications DURING the turn/start RPC,
// i.e. before TurnStart returns. It models a fast (mock) server. With the buggy ordering
// (TurnStart first, subscribe after) those notifications are published while there are no
// subscribers and are dropped with no replay; RunStreamed then hangs until ctx is cancelled.
// With the fixed ordering (subscribe before TurnStart) they are buffered and delivered.
type streamRaceTransport struct {
	notes chan transport.Notification
}

func newStreamRaceTransport() *streamRaceTransport {
	return &streamRaceTransport{notes: make(chan transport.Notification, 16)}
}

func (s *streamRaceTransport) Call(_ context.Context, method string, _ any, result any) error {
	switch method {
	case protocol.MethodThreadStart:
		return decodeFakeResult(method, Thread{ID: "th1"}, result)
	case protocol.MethodTurnStart:
		// Emit the turn's notifications before TurnStart returns.
		s.push(protocol.MethodItemAgentMessageDelta, map[string]any{
			"threadId": "th1", "turnId": "turn1", "itemId": "i1", "text": "hello",
		})
		s.push(protocol.MethodTurnCompleted, map[string]any{
			"threadId": "th1", "turnId": "turn1", "status": "completed",
		})
		// Let the notification loop publish while (under the old ordering) no subscriber
		// exists yet. This is what makes the regression deterministic rather than racy.
		time.Sleep(100 * time.Millisecond)
		return decodeFakeResult(method, Turn{ID: "turn1"}, result)
	}
	return nil
}

func (s *streamRaceTransport) push(method string, payload any) {
	raw, _ := json.Marshal(payload)
	s.notes <- transport.Notification{Method: method, Params: raw}
}

func (s *streamRaceTransport) Notify(context.Context, string, any) error { return nil }
func (s *streamRaceTransport) SetRequestHandler(RequestHandler)          {}
func (s *streamRaceTransport) Close() error                              { return nil }

// Notifications makes the transport a notificationSource so Client.New wires the event loop.
func (s *streamRaceTransport) Notifications() <-chan transport.Notification { return s.notes }

// TestRunStreamedSubscribesBeforeTurnStart pins the fix for the RunStreamed subscription race:
// events emitted before TurnStart returns must reach the stream. It fails (deltas lost, channel
// never sees turn/completed) if RunStreamed regresses to subscribing after TurnStart.
func TestRunStreamedSubscribesBeforeTurnStart(t *testing.T) {
	ft := newStreamRaceTransport()
	client, err := New(WithTransport(ft))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer st.Close()

	ch, err := st.RunStreamed(ctx, "hi")
	if err != nil {
		t.Fatalf("RunStreamed: %v", err)
	}

	var sawDelta, sawCompleted bool
	for ev := range ch {
		switch e := ev.Raw.(type) {
		case ItemAgentMessageDeltaEvent:
			if e.Text == "hello" {
				sawDelta = true
			}
		case TurnCompletedEvent:
			sawCompleted = true
		}
	}
	if !sawDelta {
		t.Error("lost item/agentMessage/delta emitted before TurnStart returned")
	}
	if !sawCompleted {
		t.Error("lost turn/completed emitted before TurnStart returned")
	}
}
