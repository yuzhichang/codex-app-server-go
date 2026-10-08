package codexgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	"github.com/zealbase/codex-app-server-go/internal/transport"
)

// Internal tests for the session supervisor. They live in package codexgo rather than
// codexgo_test because the supervisor's value is the exact call sequence it issues and the
// events it emits, and the notifications channel it needs is an internal type.

// supervisorTransport records every Call/Notify and lets the test trigger a reconnect.
type supervisorTransport struct {
	mu          sync.Mutex
	calls       []string
	reconnect   chan struct{}
	notes       chan transport.Notification
	done        chan struct{}
	closed      bool
	initFailFor int // fail the next N `initialize` calls
	turnsListed int // how many times thread/turns/list was called
	seq         int // makes generated thread ids unique
}

func newSupervisorTransport() *supervisorTransport {
	return &supervisorTransport{
		reconnect: make(chan struct{}, 1),
		notes:     make(chan transport.Notification, 8),
		done:      make(chan struct{}),
	}
}

func (s *supervisorTransport) record(entry string) {
	s.mu.Lock()
	s.calls = append(s.calls, entry)
	s.mu.Unlock()
}

func (s *supervisorTransport) methodCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *supervisorTransport) reset() {
	s.mu.Lock()
	s.calls = nil
	s.mu.Unlock()
}

// fireReconnect simulates the transport having re-dialled.
func (s *supervisorTransport) fireReconnect() {
	select {
	case s.reconnect <- struct{}{}:
	default:
	}
}

func (s *supervisorTransport) Call(_ context.Context, method string, _ any, result any) error {
	s.record("call:" + method)

	if method == protocol.MethodInitialize {
		s.mu.Lock()
		fail := s.initFailFor > 0
		if fail {
			s.initFailFor--
		}
		s.mu.Unlock()
		if fail {
			return errors.New("initialize refused")
		}
	}

	// Backfill listing. Must precede the generic thread/* branch below, which would
	// otherwise write a thread object into the turns-list response.
	if result != nil && method == protocol.MethodThreadTurnsList {
		s.mu.Lock()
		s.turnsListed++
		s.mu.Unlock()
		_ = json.Unmarshal([]byte(`{"data":[{"id":"turn-old","status":"completed"}],"nextCursor":"more"}`), result)
		return nil
	}

	// Populate results through JSON rather than type-asserting anonymous structs: the SDK
	// decodes into several different inline shapes.
	if result != nil && strings.HasPrefix(method, "thread/") {
		s.mu.Lock()
		s.seq++
		id := fmt.Sprintf("thr-%s-%d", strings.NewReplacer("/", "-").Replace(strings.TrimPrefix(method, "thread/")), s.seq)
		s.mu.Unlock()
		_ = json.Unmarshal([]byte(`{"thread":{"id":"`+id+`"}}`), result)
	}
	return nil
}

func (s *supervisorTransport) Notify(_ context.Context, method string, _ any) error {
	s.record("notify:" + method)
	return nil
}

func (s *supervisorTransport) SetRequestHandler(RequestHandler) {}

func (s *supervisorTransport) Requests() <-chan *transport.Request { return nil }
func (s *supervisorTransport) Notifications() <-chan transport.Notification {
	return s.notes
}
func (s *supervisorTransport) Done() <-chan struct{}       { return s.done }
func (s *supervisorTransport) Reconnects() <-chan struct{} { return s.reconnect }
func (s *supervisorTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	return nil
}

// collectUntil drains the subscription until want is seen or the deadline passes.
func collectUntil(sub *EventSubscription, want string, deadline time.Duration) []Event {
	var seen []Event
	timeout := time.After(deadline)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return seen
			}
			seen = append(seen, ev)
			if ev.Method == want {
				return seen
			}
		case <-timeout:
			return seen
		}
	}
}

func eventMethods(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Method)
	}
	return out
}

func newSupervisedClient(t *testing.T) (*Client, *supervisorTransport) {
	t.Helper()
	tr := newSupervisorTransport()
	client, err := New(WithTransport(tr), WithAutoReconnect())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, tr
}

// The whole point of the supervisor: a redial restores the socket, not the session. Without
// this the SDK keeps calling on a connection the server has no session for.
func TestSupervisorReEstablishesSessionAfterReconnect(t *testing.T) {
	client, tr := newSupervisedClient(t)

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeParams{ClientInfo: ClientInfo{Name: "t"}}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	first, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	second, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	if first.ID() == "" || second.ID() == "" {
		t.Fatalf("threads have no id: %q / %q", first.ID(), second.ID())
	}

	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	tr.reset()
	tr.fireReconnect()
	events := collectUntil(sub, EventMethodReconnectSucceeded, 3*time.Second)

	calls := tr.methodCalls()

	// 1. The handshake must be replayed, request before notification.
	if len(calls) < 2 || calls[0] != "call:"+protocol.MethodInitialize {
		t.Fatalf("recovery did not start with initialize: %v", calls)
	}
	if calls[1] != "notify:"+protocol.MethodInitialized {
		t.Fatalf("initialized must follow initialize: %v", calls)
	}

	// 2. Every open thread must be resumed exactly once.
	if got := countString(calls, "call:"+protocol.MethodThreadResume); got != 2 {
		t.Fatalf("thread/resume issued %d times, want 2 (one per open thread): %v", got, calls)
	}

	// 3. The events must report what happened.
	methods := eventMethods(events)
	for _, want := range []string{
		EventMethodReconnectStarted, EventMethodSessionRecovered, EventMethodReconnectSucceeded,
	} {
		if !containsString(methods, want) {
			t.Errorf("missing %q; saw %v", want, methods)
		}
	}

	// The recovered event must name the threads, or an operator cannot tell what came back.
	for _, ev := range events {
		if ev.Method != EventMethodSessionRecovered {
			continue
		}
		rec, ok := ev.Value.(SessionRecoveredEvent)
		if !ok {
			t.Fatalf("recovered event value = %T", ev.Value)
		}
		if len(rec.ThreadsResumed) != 2 {
			t.Fatalf("ThreadsResumed = %v, want both threads", rec.ThreadsResumed)
		}
		if len(rec.ThreadsFailed) != 0 {
			t.Fatalf("ThreadsFailed = %v, want none", rec.ThreadsFailed)
		}
	}

	// A closed thread must not be resumed afterwards: it is no longer the caller's concern.
	first.Close()
	tr.reset()
	tr.fireReconnect()
	collectUntil(sub, EventMethodReconnectSucceeded, 3*time.Second)

	if got := countString(tr.methodCalls(), "call:"+protocol.MethodThreadResume); got != 1 {
		t.Fatalf("after closing one thread, resume issued %d times, want 1: %v",
			got, tr.methodCalls())
	}
}

// A failed handshake must be reported and retried, not silently abandoned -- otherwise the
// session stays broken while the SDK looks idle.
func TestSupervisorRetriesAfterFailedHandshake(t *testing.T) {
	client, tr := newSupervisedClient(t)

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeParams{ClientInfo: ClientInfo{Name: "t"}}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Inject the failure only now: the setup handshake above must succeed.
	tr.initFailFor = 1

	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	tr.reset()
	tr.fireReconnect()
	events := collectUntil(sub, EventMethodReconnectSucceeded, 5*time.Second)
	methods := eventMethods(events)

	if !containsString(methods, EventMethodReconnectFailed) {
		t.Fatalf("a failed round was not reported; saw %v", methods)
	}
	if !containsString(methods, EventMethodReconnectSucceeded) {
		t.Fatalf("the supervisor gave up instead of retrying; saw %v", methods)
	}

	// The failure event must carry the cause, or the operator cannot act on it.
	for _, ev := range events {
		if ev.Method != EventMethodReconnectFailed {
			continue
		}
		f, ok := ev.Value.(ReconnectFailedEvent)
		if !ok {
			t.Fatalf("failed event value = %T", ev.Value)
		}
		if f.Err == nil || !strings.Contains(f.Err.Error(), "initialize refused") {
			t.Fatalf("failure cause not surfaced: %v", f.Err)
		}
		if f.Attempt < 1 {
			t.Fatalf("Attempt = %d, want >= 1", f.Attempt)
		}
	}
}

// Requesting auto-reconnect on a transport that cannot report reconnections must fail loudly:
// silently doing nothing would leave the caller believing sessions were being recovered.
func TestAutoReconnectRequiresReconnectableTransport(t *testing.T) {
	_, err := New(WithTransport(plainTransport{}), WithAutoReconnect())
	if err == nil {
		t.Fatal("expected an error for a transport that cannot reconnect")
	}
	if !strings.Contains(err.Error(), "WithAutoReconnect") {
		t.Fatalf("error should name the option: %v", err)
	}
}

// plainTransport satisfies Transport but cannot report reconnections.
type plainTransport struct{}

func (plainTransport) Call(context.Context, string, any, any) error { return nil }
func (plainTransport) Notify(context.Context, string, any) error    { return nil }
func (plainTransport) SetRequestHandler(RequestHandler)             {}
func (plainTransport) Close() error                                 { return nil }

func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func countString(hay []string, needle string) int {
	n := 0
	for _, s := range hay {
		if s == needle {
			n++
		}
	}
	return n
}

// A reconnect cannot replay what was missed -- upstream has no replay -- so the remedy is to
// hand over authoritative history for the resumed threads.
func TestReconnectBackfillsResumedThreads(t *testing.T) {
	client, tr := newSupervisedClient(t)

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeParams{ClientInfo: ClientInfo{Name: "t"}}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	thread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	tr.reset()
	tr.fireReconnect()

	var backfilled *SessionBackfilledEvent
	deadline := time.After(3 * time.Second)
	for backfilled == nil {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Method == EventMethodSessionBackfilled {
				got, isBackfill := ev.Value.(SessionBackfilledEvent)
				if !isBackfill {
					t.Fatalf("value = %T", ev.Value)
				}
				backfilled = &got
			}
		case <-deadline:
			t.Fatal("no sdk/sessionBackfilled event after the reconnect")
		}
	}

	if backfilled.ThreadID != thread.ID() {
		t.Errorf("ThreadID = %q, want %q", backfilled.ThreadID, thread.ID())
	}
	// The turns come across as the runtime Turn type, not the generated one.
	if len(backfilled.Turns) != 1 || backfilled.Turns[0].ID != "turn-old" {
		t.Fatalf("turns = %+v, want the one listed turn", backfilled.Turns)
	}
	// A non-empty next cursor means older turns exist and were not re-read.
	if !backfilled.Truncated {
		t.Error("Truncated = false despite a next cursor")
	}
	if tr.turnsListed == 0 {
		t.Error("thread/turns/list was never called")
	}
}

// Backfilling costs one listing call per resumed thread per reconnect, so it must be
// possible to turn off.
func TestSessionBackfillCanBeDisabled(t *testing.T) {
	tr := newSupervisorTransport()
	client, err := New(WithTransport(tr), WithAutoReconnect(), WithSessionBackfill(0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeParams{ClientInfo: ClientInfo{Name: "t"}}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := client.StartThread(ctx); err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	tr.reset()
	tr.fireReconnect()
	collectUntil(sub, EventMethodReconnectSucceeded, 3*time.Second)

	if tr.turnsListed != 0 {
		t.Fatalf("thread/turns/list called %d times despite WithSessionBackfill(0)", tr.turnsListed)
	}
}

// A negative limit is rejected rather than silently meaning something else.
func TestSessionBackfillRejectsNegativeLimit(t *testing.T) {
	if _, err := New(WithTransport(newSupervisorTransport()), WithAutoReconnect(), WithSessionBackfill(-1)); err == nil {
		t.Fatal("expected an error for a negative backfill limit")
	}
}
