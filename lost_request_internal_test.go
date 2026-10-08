package codexgo

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/transport"
)

// newInternalTestClient builds a client over the recording stub transport, which doubles as
// a notificationSource so the event broker is created.
func newInternalTestClient(t *testing.T) *Client {
	t.Helper()
	// newChannelTransport is what New() wraps option-built transports in, and it is the
	// type the onRequestLost wiring targets -- passing the stub straight to WithTransport
	// would bypass both.
	st := newChannelTransport(newSupervisorTransport())
	client, err := New(WithTransport(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// A server request whose answer never reaches the server is the one failure a handler
// cannot observe from inside: it decided something (perhaps approved a command), and the
// decision was discarded. Upstream has no replay, so the only useful action is to say so.
func TestPendingApprovalLostIsReported(t *testing.T) {
	client := newInternalTestClient(t)
	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	st, ok := client.transport.(*stdioTransport)
	if !ok {
		t.Fatalf("transport is %T, expected *stdioTransport", client.transport)
	}
	if st.onRequestLost == nil {
		t.Fatal("New() did not wire onRequestLost; a lost answer would be silent")
	}

	st.reportRequestLost(
		"item/commandExecution/requestApproval",
		json.RawMessage(`{"threadId":"t-1","turnId":"turn-1"}`),
		errors.New("connection closed"),
	)

	select {
	case ev, ok := <-sub.C():
		if !ok {
			t.Fatal("event stream closed")
		}
		if ev.Method != EventMethodPendingApprovalLost {
			t.Fatalf("method = %q, want %q", ev.Method, EventMethodPendingApprovalLost)
		}
		lost, ok := ev.Value.(PendingApprovalLostEvent)
		if !ok {
			t.Fatalf("value = %T", ev.Value)
		}
		// The method is what tells the caller which decision was discarded...
		if lost.Method != "item/commandExecution/requestApproval" {
			t.Errorf("Method = %q", lost.Method)
		}
		// ...and the ids are what let them tie it back to a turn.
		if lost.ThreadID != "t-1" || lost.TurnID != "turn-1" {
			t.Errorf("thread/turn not extracted: %+v", lost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no sdk/pendingApprovalLost event was emitted")
	}
}

// A second reply to the same request is a programming error, not a lost answer, and must not
// be reported as one.
func TestAlreadyRepliedIsNotReportedAsLost(t *testing.T) {
	client := newInternalTestClient(t)
	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	st := client.transport.(*stdioTransport)
	st.reportRequestLost("applyPatchApproval", json.RawMessage(`{}`), transport.ErrAlreadyReplied)

	select {
	case ev := <-sub.C():
		t.Fatalf("double reply was reported as a lost request: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

// The loss report bounds the window the dropped events were offered in. Upstream carries no
// sequence numbers, so position is not derivable -- but time is, and it is enough to
// correlate the loss with whatever else was happening.
func TestEventsLostErrorCarriesGapWindow(t *testing.T) {
	cfg := testBrokerConfig()
	cfg.outCap = 2
	cfg.stallTimeout = 5 * time.Second // isolate from stall: we want the Close path
	b := newEventBroker(cfg)
	defer b.close()

	sub := b.Subscribe()

	// The first event fills the one ordinary slot (outCap-1 == 1) and is *delivered*, so it
	// is not part of the loss. The window therefore spans only what is still queued: e2 and
	// e3, published 120ms apart.
	b.publish(Event{Method: "e1"})
	time.Sleep(30 * time.Millisecond)
	b.publish(Event{Method: "e2"})
	time.Sleep(120 * time.Millisecond)
	b.publish(Event{Method: "e3"})

	sub.Close()
	select {
	case <-sub.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not terminate")
	}

	var lost *EventsLostError
	if !errors.As(sub.Err(), &lost) {
		t.Fatalf("Err() = %v, want *EventsLostError", sub.Err())
	}
	if lost.LostCount == 0 {
		t.Fatal("LostCount = 0 despite undelivered events")
	}
	if lost.GapFrom.IsZero() || lost.GapTo.IsZero() {
		t.Fatalf("gap window not reported: %v..%v", lost.GapFrom, lost.GapTo)
	}
	if lost.GapTo.Before(lost.GapFrom) {
		t.Fatalf("gap window inverted: %v..%v", lost.GapFrom, lost.GapTo)
	}
	// The window must reflect the real spacing, not collapse to a point.
	if d := lost.GapTo.Sub(lost.GapFrom); d < 100*time.Millisecond {
		t.Fatalf("gap window too narrow to be real: %v", d)
	}
}
