package codexgo

import (
	"context"
	"sync"
)

// sessionGate blocks normal RPCs while a reconnect's protocol recovery is in progress.
//
// A reconnect restores the socket, not the session: the app-server treats the new connection
// as a brand-new client, so until the initialize handshake has been replayed and every open
// thread resumed, a normal call would be sent to a connection the server has no session for.
// Worse, it may appear to succeed against a fresh, empty session.
//
// The transport arms the gate *before* it publishes a newly re-dialed socket (so the window is
// not merely small but closed), and the session supervisor releases it once recovery completes.
// A transport that cannot arm it directly (no SetOnReconnect) is covered by the supervisor
// arming it when the reconnect signal arrives -- best-effort, but still bounds the whole
// recovery window.
type sessionGate struct {
	inner Transport

	mu    sync.Mutex
	ready chan struct{} // closed while normal calls may proceed
}

func newSessionGate(inner Transport) *sessionGate {
	g := &sessionGate{inner: inner, ready: make(chan struct{})}
	close(g.ready) // open until a reconnect happens
	return g
}

// arm closes the gate so subsequent Call/Notify wait. Idempotent: arming an already-armed gate
// keeps the same open channel, so a nested reconnect does not strand a waiter.
func (g *sessionGate) arm() {
	g.mu.Lock()
	select {
	case <-g.ready:
		g.ready = make(chan struct{})
	default:
	}
	g.mu.Unlock()
}

// release reopens the gate, letting blocked calls through. Idempotent.
func (g *sessionGate) release() {
	g.mu.Lock()
	select {
	case <-g.ready:
	default:
		close(g.ready)
	}
	g.mu.Unlock()
}

func (g *sessionGate) wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	ready := g.ready
	g.mu.Unlock()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *sessionGate) Call(ctx context.Context, method string, params, result any) error {
	if err := g.wait(ctx); err != nil {
		return err
	}
	return g.inner.Call(ctx, method, params, result)
}

func (g *sessionGate) Notify(ctx context.Context, method string, params any) error {
	if err := g.wait(ctx); err != nil {
		return err
	}
	return g.inner.Notify(ctx, method, params)
}

func (g *sessionGate) SetRequestHandler(h RequestHandler) { g.inner.SetRequestHandler(h) }
func (g *sessionGate) Close() error                       { return g.inner.Close() }
