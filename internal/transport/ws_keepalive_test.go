package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// The keepalive must not disturb a healthy connection: pings are control frames, and a
// ping loop that deadlocks on the write lock or misreads the pong would show up here as a
// failed or hung Call.
func TestWebSocketKeepaliveKeepsHealthyConnectionUsable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(data, &req)
			reply := fmt.Sprintf(`{"id":%s,"result":{"ok":true}}`, req.ID)
			if err := c.Write(ctx, websocket.MessageText, []byte(reply)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	// Ping far more often than a healthy test would normally produce traffic, so several
	// ping/pong cycles fit inside this test.
	w, err := NewWebSocket(context.Background(), wsURL(srv.URL), WithWSPingInterval(15*time.Millisecond))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer w.Close()

	// Let several ping intervals elapse before the first RPC.
	time.Sleep(6 * 15 * time.Millisecond)

	select {
	case <-w.Done():
		t.Fatal("keepalive killed a healthy connection")
	default:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out struct {
		OK bool `json:"ok"`
	}
	if err := w.Call(ctx, "ping", nil, &out); err != nil {
		t.Fatalf("Call after several keepalives: %v", err)
	}
	if !out.OK {
		t.Fatal("unexpected result")
	}
}

// A failed keepalive must terminate the transport, otherwise a proxy-dropped connection
// stays "open" until the next RPC, which may be minutes later.
func TestWebSocketKeepaliveTerminatesOnDeadPeer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		// Close immediately without a close handshake, so pongs stop forever while the
		// socket has not necessarily been observed as closed by the reader yet.
		_ = c.CloseNow()
	}))
	defer srv.Close()

	w, err := NewWebSocket(context.Background(), wsURL(srv.URL), WithWSPingInterval(10*time.Millisecond))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer w.Close()

	select {
	case <-w.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("connection with a dead peer was never terminated")
	}
}

// T2.1: during the re-dial window `current` still points at the dead transport. Call must
// wait for the replacement instead of failing (or hanging) on a socket that will never
// carry anything again.
func TestReconnectingWSWaitsOutRedialWindow(t *testing.T) {
	oldInit := reconnectInitialBackoffNanos.Load()
	reconnectInitialBackoffNanos.Store(int64(10 * time.Millisecond))
	defer reconnectInitialBackoffNanos.Store(oldInit)

	var conns atomic.Int32
	firstServed := make(chan struct{})
	allowRedial := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		n := conns.Add(1)
		if n > 1 {
			// Refuse every re-dial until the test opens the gate, so the re-dial window is
			// observably wide instead of closing before the Call arrives.
			select {
			case <-allowRedial:
			default:
				rw.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		c, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		if n == 1 {
			// Die shortly after the handshake so the supervisor has to recover.
			time.Sleep(20 * time.Millisecond)
			close(firstServed)
			return
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(data, &req)
			reply := fmt.Sprintf(`{"id":%s,"result":{"ok":true}}`, req.ID)
			if err := c.Write(ctx, websocket.MessageText, []byte(reply)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	r, err := NewReconnectingWS(context.Background(), wsURL(srv.URL))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer r.Close()

	<-firstServed
	// Give watchLoop time to notice the death and enter the re-dial loop.
	time.Sleep(60 * time.Millisecond)

	type result struct {
		out struct {
			OK bool `json:"ok"`
		}
		err error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var out struct {
			OK bool `json:"ok"`
		}
		err := r.Call(ctx, "ping", nil, &out)
		done <- result{out: out, err: err}
	}()

	// The Call must still be outstanding while the re-dial is being refused.
	select {
	case res := <-done:
		t.Fatalf("Call returned during the re-dial window: err=%v", res.err)
	case <-time.After(120 * time.Millisecond):
	}

	close(allowRedial)

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Call after re-dial: %v", res.err)
		}
		if !res.out.OK {
			t.Fatal("unexpected result after re-dial")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call never completed after the connection was restored")
	}
}
