package codexgo

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
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
)

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
}

func newSessionSupervisor(c *Client) *sessionSupervisor {
	return &sessionSupervisor{
		client:         c,
		threads:        make(map[string]struct{}),
		recoverTimeout: 30 * time.Second,
		baseBackoff:    500 * time.Millisecond,
		maxBackoff:     30 * time.Second,
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
	if _, err := s.client.Initialize(attemptCtx, req); err != nil {
		return nil, nil, fmt.Errorf("re-initialize: %w", err)
	}

	// 2. Resume the threads that were open on the previous connection. Upstream destroys
	//    subscriptions with the connection and offers no replay, so resumption is the only
	//    way back.
	for _, id := range s.trackedThreads() {
		if _, err := s.client.ThreadResume(attemptCtx, ThreadResumeParams{ThreadID: id}); err != nil {
			// One thread that refuses to resume must not cost the others their recovery.
			failed = append(failed, id)
			continue
		}
		resumed = append(resumed, id)
	}
	return resumed, failed, nil
}

func (s *sessionSupervisor) emit(method string, v any) {
	if s == nil || s.client == nil || s.client.events == nil {
		return
	}
	s.client.events.publish(Event{Method: method, Value: v})
}
