package transport

import (
	"context"
	"sync"
	"time"
)

// ReconnectingWS wraps a WebSocketTransport and automatically re-dials if the
// connection drops. Pending Call operations on a dropped connection fail;
// callers should layer RetryTransport on top to retry at the RPC level.
type ReconnectingWS struct {
	url  string
	opts []WSOption

	mu      sync.RWMutex
	current *WebSocketTransport
	// ready is closed while `current` is usable and replaced with a fresh open channel the
	// moment the current connection is observed dead, so Call can wait out a re-dial rather
	// than being handed the dead transport.
	ready chan struct{}

	notes    chan Notification
	requests chan *Request
	done     chan struct{}
	doneOnce sync.Once

	closedMu sync.Mutex
	closed   bool
}

// NewReconnectingWS dials url and returns a transport that re-dials on drop,
// reusing the same url and options for each reconnect.
func NewReconnectingWS(ctx context.Context, url string, opts ...WSOption) (*ReconnectingWS, error) {
	first, err := NewWebSocket(ctx, url, opts...)
	if err != nil {
		return nil, err
	}
	r := &ReconnectingWS{
		url:      url,
		opts:     opts,
		current:  first,
		ready:    make(chan struct{}),
		notes:    make(chan Notification, 32),
		requests: make(chan *Request, 32),
		done:     make(chan struct{}),
	}
	close(r.ready) // the initial connection is usable
	go r.fanNotifications(first)
	go r.fanRequests(first)
	go r.watchLoop()
	return r, nil
}

func (r *ReconnectingWS) isClosed() bool {
	r.closedMu.Lock()
	defer r.closedMu.Unlock()
	return r.closed
}

func (r *ReconnectingWS) watchLoop() {
	backoff := reconnectInitialBackoff()
	for {
		r.mu.RLock()
		cur := r.current
		r.mu.RUnlock()

		select {
		case <-cur.Done():
		case <-r.done:
			return
		}

		if r.isClosed() {
			return
		}

		// Mark not-ready before re-dialing: from here until the replacement is installed,
		// Call must wait rather than use the dead transport.
		r.mu.Lock()
		r.ready = make(chan struct{})
		r.mu.Unlock()

		// Re-dial, retrying with back-off until it succeeds or we close.
		var next *WebSocketTransport
		for {
			select {
			case <-time.After(backoff):
			case <-r.done:
				return
			}
			if r.isClosed() {
				return
			}

			backoff *= 2
			if max := reconnectMaxBackoff(); backoff > max {
				backoff = max
			}

			ws, err := NewWebSocket(context.Background(), r.url, r.opts...)
			if err == nil {
				next = ws
				break
			}
		}

		r.mu.Lock()
		r.current = next
		close(r.ready)
		r.mu.Unlock()

		go r.fanNotifications(next)
		go r.fanRequests(next)

		backoff = reconnectInitialBackoff()
	}
}

func (r *ReconnectingWS) fanNotifications(t *WebSocketTransport) {
	src := t.Notifications()
	for {
		select {
		case n, ok := <-src:
			if !ok {
				return
			}
			select {
			case r.notes <- n:
			case <-r.done:
				return
			}
		case <-t.Done():
			return
		case <-r.done:
			return
		}
	}
}

func (r *ReconnectingWS) fanRequests(t *WebSocketTransport) {
	src := t.Requests()
	for {
		select {
		case req, ok := <-src:
			if !ok {
				return
			}
			select {
			case r.requests <- req:
			case <-r.done:
				return
			}
		case <-t.Done():
			return
		case <-r.done:
			return
		}
	}
}

// Call delegates to the current inner transport, waiting out a re-dial if one is in progress.
//
// The wait matters: during the window between a connection dropping and its replacement being
// installed, `current` still points at the dead transport, so a naive delegation would fail
// immediately -- or hang on a socket that will never carry anything again. Callers that want
// the failure surfaced instead of absorbed should pass a ctx with a deadline.
func (r *ReconnectingWS) Call(ctx context.Context, method string, params any, result any) error {
	for {
		r.mu.RLock()
		ready := r.ready
		r.mu.RUnlock()

		select {
		case <-ready:
			// Read current *after* readiness: it may have been replaced while we waited.
			r.mu.RLock()
			cur := r.current
			r.mu.RUnlock()
			return cur.Call(ctx, method, params, result)
		case <-ctx.Done():
			return ctx.Err()
		case <-r.done:
			return ErrClosed
		}
	}
}

// Notify delegates to the current inner transport.
func (r *ReconnectingWS) Notify(ctx context.Context, method string, params any) error {
	r.mu.RLock()
	cur := r.current
	r.mu.RUnlock()
	return cur.Notify(ctx, method, params)
}

// Requests returns the stable channel of server-initiated requests.
func (r *ReconnectingWS) Requests() <-chan *Request { return r.requests }

// Notifications returns the stable channel of server notifications.
func (r *ReconnectingWS) Notifications() <-chan Notification { return r.notes }

// Done is closed when the reconnecting transport is permanently closed.
func (r *ReconnectingWS) Done() <-chan struct{} { return r.done }

// Close permanently shuts down the transport and the current inner connection.
func (r *ReconnectingWS) Close() error {
	r.closedMu.Lock()
	if r.closed {
		r.closedMu.Unlock()
		return nil
	}
	r.closed = true
	r.closedMu.Unlock()

	r.doneOnce.Do(func() { close(r.done) })

	r.mu.RLock()
	cur := r.current
	r.mu.RUnlock()
	return cur.Close()
}

// Ensure ReconnectingWS satisfies Transport at compile time.
var _ Transport = (*ReconnectingWS)(nil)
