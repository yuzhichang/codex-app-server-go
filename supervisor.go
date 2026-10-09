package codexgo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
)

// Synthetic method names for supervisor-reported events.
//
// These are NOT wire notifications -- no such methods exist upstream, and enabling
// auto-reconnect does not cause the server to send anything extra. They are delivered on the
// same EventSubscription as real events so one consumer loop sees everything, and the `sdk/`
// prefix makes it unmistakable that they are synthesised locally.
const (
	// EventMethodReconnectStarted precedes each recovery round.
	EventMethodReconnectStarted = "sdk/reconnectStarted"
	// EventMethodReconnectSucceeded means the handshake and thread resumption completed.
	EventMethodReconnectSucceeded = "sdk/reconnectSucceeded"
	// EventMethodReconnectFailed reports one failed round; the supervisor keeps retrying.
	EventMethodReconnectFailed = "sdk/reconnectFailed"
	// EventMethodSessionRecovered reports exactly which threads came back.
	EventMethodSessionRecovered = "sdk/sessionRecovered"
	// EventMethodPendingApprovalLost reports a server-initiated request that can never be
	// answered because the connection carrying it went away.
	EventMethodPendingApprovalLost = "sdk/pendingApprovalLost"
	// EventMethodUnhandledServerRequest reports a server request the SDK answered on the
	// caller's behalf, because no handler was configured for it.
	EventMethodUnhandledServerRequest = "sdk/unhandledServerRequest"
	// EventMethodSessionBackfilled reports authoritative history pulled after a reconnect.
	EventMethodSessionBackfilled = "sdk/sessionBackfilled"
)

// SessionBackfilledEvent carries the history re-read for one thread after a reconnect.
//
// Notifications sent while the connection was down are gone: upstream has no replay, so
// nothing can re-deliver them. This event is the remedy -- it hands over the authoritative
// current state of the thread so a consumer can reconcile.
//
// It is deliberately *history*, not a replay of the missed notifications. Re-emitting
// wire-shaped events would be indistinguishable from live ones, so a consumer that had
// already received part of a turn before the drop would double-count it. Treat Turns as
// "the server's word on what happened", and merge by turn id.
type SessionBackfilledEvent struct {
	ThreadID string
	// Turns is the most recent page of turns, newest first (the server's default order).
	Turns []Turn
	// Truncated is true when the page limit was reached and older turns exist. Those are
	// older than the drop, so they were not missed -- but they are one thread/turns/list
	// call away if you need them.
	Truncated bool
}

// UnhandledServerRequestEvent reports a server-initiated request that reached no configured
// handler, and what the SDK replied instead.
//
// This is the observability half of decision R9: refusing a request must not end the session,
// but it must not be silent either, or an application would never learn that the server
// asked it something.
type UnhandledServerRequestEvent struct {
	Method string
	// Action is what the SDK replied: "declined" (a valid protocol refusal) or
	// "methodNotFound" (the SDK has no idea what the method is).
	Action string
	// Reason explains the choice, for logs.
	Reason string
	// ThreadID and TurnID are best-effort: most payloads carry them, but not all do.
	ThreadID string
	TurnID   string
}

// ReconnectStartedEvent is emitted when the SDK begins re-establishing protocol state.
type ReconnectStartedEvent struct {
	// Attempt is 1-based and counts recovery rounds, not internal retries.
	Attempt int
}

// ReconnectSucceededEvent is emitted once protocol state has been fully re-established.
type ReconnectSucceededEvent struct {
	Attempt int
	// ThreadsResumed counts the threads that were successfully resumed.
	ThreadsResumed int
	// ThreadsFailed lists threads that could not be resumed. Their server-side history is
	// untouched, but this connection no longer has them open.
	ThreadsFailed []string
}

// ReconnectFailedEvent reports a failed recovery round.
//
// Reconnection is not abandoned after one failure: the supervisor retries with exponential
// backoff until the client is closed, emitting one of these per failed round.
type ReconnectFailedEvent struct {
	Attempt int
	Err     error
}

// SessionRecoveredEvent names the threads that were resumed, and those that were not.
type SessionRecoveredEvent struct {
	Attempt        int
	ThreadsResumed []string
	ThreadsFailed  []string
}

// PendingApprovalLostEvent reports a server-initiated request that was lost with its
// connection.
//
// The session does not fail, but the request can never be answered, so the SDK says so rather
// than leaving a handler waiting for a reply that will not come. Upstream has no replay, so
// there is nothing to re-deliver.
type PendingApprovalLostEvent struct {
	ThreadID string
	TurnID   string
	Method   string
}

// reconnectNotify is implemented by transports that re-establish their own connection and
// report each success (e.g. ReconnectingWS).
type reconnectNotify interface {
	Reconnects() <-chan struct{}
}

// sessionSupervisor re-establishes protocol state after a transport-level reconnect.
//
// A reconnect restores the socket only. The app-server treats a new connection as a brand
// new client, so without replaying the initialize handshake and resuming every open thread,
// the SDK would keep issuing calls on a connection the server has no session for. Those
// calls do not fail cleanly -- they can appear to succeed against a fresh, empty session,
// which is the worse outcome.
//
// This is why auto-reconnect is not merely "the transport redials".
type sessionSupervisor struct {
	client *Client

	mu      sync.Mutex
	threads map[string]struct{}
	attempt int
	initReq InitializeParams

	// recoverTimeout bounds one recovery round, so a half-open connection cannot wedge the
	// supervisor forever.
	//
	// These are read by the supervisor's goroutine, which starts inside New(), so they must
	// never be mutated afterwards -- that would be a data race. Configure them before the
	// goroutine starts, or not at all.
	recoverTimeout time.Duration
	baseBackoff    time.Duration
	maxBackoff     time.Duration
	// backfillLimit is how many turns to re-read per thread after a reconnect. 0 disables
	// backfilling entirely.
	backfillLimit int64
}

// defaultBackfillTurns bounds how much history a reconnect pulls per thread. The point is to
// re-establish a recent, authoritative view -- not to mirror the whole thread, which is one
// thread/turns/list call away if the caller wants it.
const defaultBackfillTurns = 20

func newSessionSupervisor(c *Client) *sessionSupervisor {
	return &sessionSupervisor{
		client:         c,
		threads:        make(map[string]struct{}),
		recoverTimeout: 30 * time.Second,
		baseBackoff:    500 * time.Millisecond,
		maxBackoff:     30 * time.Second,
		backfillLimit:  defaultBackfillTurns,
	}
}

// setInitRequest records the handshake to replay. Called by Initialize.
func (s *sessionSupervisor) setInitRequest(req InitializeParams) {
	s.mu.Lock()
	s.initReq = req
	s.mu.Unlock()
}

// trackThread records a thread as open on this connection so it can be resumed after a
// reconnect. Idempotent.
func (s *sessionSupervisor) trackThread(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	s.threads[id] = struct{}{}
	s.mu.Unlock()
}

func (s *sessionSupervisor) untrackThread(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.threads, id)
	s.mu.Unlock()
}

func (s *sessionSupervisor) trackedThreads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.threads))
	for id := range s.threads {
		out = append(out, id)
	}
	// Sorted so recovery order (and therefore the emitted event) is deterministic.
	sort.Strings(out)
	return out
}

// Run consumes reconnect signals until ctx is done.
func (s *sessionSupervisor) run(ctx context.Context, signals <-chan struct{}) {
	backoff := s.baseBackoff
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
		}
		// Hold normal RPCs for the whole recovery. The transport usually armed the gate already
		// (before publishing the socket); this covers a transport that cannot.
		s.setGate(true)
		for {
			s.mu.Lock()
			s.attempt++
			attempt := s.attempt
			s.mu.Unlock()

			s.emit(EventMethodReconnectStarted, ReconnectStartedEvent{Attempt: attempt})
			resumed, failed, err := s.recoverOnce(ctx, attempt)
			if err == nil {
				s.emit(EventMethodSessionRecovered, SessionRecoveredEvent{
					Attempt: attempt, ThreadsResumed: resumed, ThreadsFailed: failed,
				})
				s.emit(EventMethodReconnectSucceeded, ReconnectSucceededEvent{
					Attempt: attempt, ThreadsResumed: len(resumed), ThreadsFailed: failed,
				})
				s.setGate(false)
				backoff = s.baseBackoff
				break
			}
			s.emit(EventMethodReconnectFailed, ReconnectFailedEvent{Attempt: attempt, Err: err})

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > s.maxBackoff {
				backoff = s.maxBackoff
			}
		}
	}
}

// recoverOnce replays the handshake and resumes every tracked thread.
func (s *sessionSupervisor) recoverOnce(ctx context.Context, attempt int) (resumed, failed []string, err error) {
	s.mu.Lock()
	req := s.initReq
	s.mu.Unlock()

	attemptCtx, cancel := context.WithTimeout(ctx, s.recoverTimeout)
	defer cancel()

	// 1. Handshake. Required even though the transport is connected: without it the server
	//    has no session for this connection.
	if err := s.recoverInitialize(attemptCtx, req); err != nil {
		return nil, nil, fmt.Errorf("re-initialize: %w", err)
	}

	// 2. Resume the threads that were open on the previous connection. Upstream destroys
	//    subscriptions with the connection and offers no replay, so resumption is the only
	//    way back.
	for _, id := range s.trackedThreads() {
		if err := s.recoverResume(attemptCtx, id); err != nil {
			// One thread that refuses to resume must not cost the others their recovery.
			failed = append(failed, id)
			continue
		}
		resumed = append(resumed, id)
		s.backfill(attemptCtx, id)
	}
	return resumed, failed, nil
}

// backfill pulls the thread's recent turns so a consumer can reconcile after the drop.
//
// Failures here are reported through the same reconnect-failure channel rather than being
// swallowed: a recovered session whose history could not be re-read is not the same as one
// that could, and the caller needs to know which they got.
func (s *sessionSupervisor) backfill(ctx context.Context, threadID string) {
	if s.backfillLimit <= 0 {
		return
	}
	resp, err := func() (ThreadTurnsListResponse, error) {
		var r ThreadTurnsListResponse
		err := s.client.rawTransport.Call(ctx, protocol.MethodThreadTurnsList, ThreadTurnsListParams{
			ThreadID: threadID,
			Limit:    s.backfillLimit,
		}, &r)
		return r, err
	}()
	if err != nil {
		s.emit(EventMethodReconnectFailed, ReconnectFailedEvent{
			Attempt: s.currentAttempt(),
			Err:     fmt.Errorf("backfill %s: %w", threadID, err),
		})
		return
	}
	turns, err := turnsFromSchema(resp.Data)
	if err != nil {
		s.emit(EventMethodReconnectFailed, ReconnectFailedEvent{
			Attempt: s.currentAttempt(),
			Err:     fmt.Errorf("backfill %s: converting turns: %w", threadID, err),
		})
		return
	}
	s.emit(EventMethodSessionBackfilled, SessionBackfilledEvent{
		ThreadID:  threadID,
		Turns:     turns,
		Truncated: resp.NextCursor != "",
	})
}

// turnsFromSchema converts the generated Turn type used by the paginated listing RPCs into
// the runtime Turn the rest of the SDK exposes.
//
// The two are not interchangeable: protocol.Turn parses timestamps flexibly and keeps the raw
// payload, which the generated struct does not. Conversion goes through JSON rather than
// field-by-field so the two cannot silently drift apart -- a new field lands correctly on
// both sides or fails loudly here.
func turnsFromSchema(in []schematypes.Turn) ([]Turn, error) {
	if len(in) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out []Turn
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// setGate arms (armed=true) or releases the session gate. No-op when auto-reconnect is off.
func (s *sessionSupervisor) setGate(armed bool) {
	if s == nil || s.client == nil || s.client.gate == nil {
		return
	}
	if armed {
		s.client.gate.arm()
	} else {
		s.client.gate.release()
	}
}

// recoverInitialize replays the handshake over the RAW transport. Recovery must bypass the
// session gate: routing it through the gated transport would wait on the very barrier the
// recovery is meant to release, and deadlock.
func (s *sessionSupervisor) recoverInitialize(ctx context.Context, req InitializeParams) error {
	var result InitializeResponse
	if err := s.client.rawTransport.Call(ctx, protocol.MethodInitialize, req, &result); err != nil {
		return err
	}
	return s.client.rawTransport.Notify(ctx, protocol.MethodInitialized, nil)
}

// recoverResume re-opens one thread over the RAW transport (see recoverInitialize).
func (s *sessionSupervisor) recoverResume(ctx context.Context, threadID string) error {
	var resp struct {
		Thread Thread `json:"thread"`
	}
	return s.client.rawTransport.Call(ctx, protocol.MethodThreadResume, ThreadResumeParams{ThreadID: threadID}, &resp)
}

func (s *sessionSupervisor) currentAttempt() int {	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempt
}

func (s *sessionSupervisor) emit(method string, v any) {
	if s == nil || s.client == nil || s.client.events == nil {
		return
	}
	s.client.events.publish(Event{Method: method, Value: v})
}
