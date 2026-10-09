package codexgo_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// The method a "normal" (non-recovery) call issues in these tests.
const barrierNormalMethod = "thread/loaded/list"

// suspendReconnect brings up a reconnecting client with one open thread, drops the connection,
// and returns once the reconnect handshake has reached the server with its `initialize` reply
// suspended. release resumes it (idempotent); methods yields every method the server records,
// in order, from that point on.
//
// Everything is ordered by channels: the caller waits on the server's method log rather than
// sleeping, and the suspension is explicit.
func suspendReconnect(t *testing.T) (client *codexgo.Client, release func(), methods <-chan string) {
	t.Helper()

	server := &fakeAppServer{
		initBlocked: make(chan struct{}),
		onMethod:    make(chan string, 256),
	}
	srv := httptest.NewServer(server.handler())
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, err := codexgo.New(
		codexgo.WithReconnectingWSTransport(context.Background(), url),
		codexgo.WithAutoReconnect(),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	thread, err := c.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	t.Cleanup(thread.Close)

	// Drain the initial handshake, then arm the suspension and drop. Arming after the initial
	// handshake matters: arming earlier would suspend the very first initialize too.
	drainMethods(server.onMethod)
	block := make(chan struct{})
	server.setBlockInit(block)
	server.drop()

	// The reconnect handshake must reach the server and block in initialize.
	awaitMethod(t, server.onMethod, "initialize", 5*time.Second)
	select {
	case <-server.initBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("the reconnect handshake never blocked in initialize")
	}

	var once sync.Once
	return c, func() { once.Do(func() { close(block) }) }, server.onMethod
}

func drainMethods(ch <-chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func awaitMethod(t *testing.T, ch <-chan string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m := <-ch:
			if m == want {
				return
			}
		case <-deadline:
			t.Fatalf("server never received %q", want)
		}
	}
}

func assertSeenBefore(t *testing.T, seq []string, first, second string) {
	t.Helper()
	iFirst, iSecond := -1, -1
	for i, m := range seq {
		if m == first && iFirst < 0 {
			iFirst = i
		}
		if m == second && iSecond < 0 {
			iSecond = i
		}
	}
	if iFirst < 0 || iSecond < 0 {
		t.Fatalf("expected both %q and %q in %v", first, second, seq)
	}
	if iFirst >= iSecond {
		t.Fatalf("%q reached the server before recovery's %q: %v", second, first, seq)
	}
}

// The barrier must be in effect before the new socket is visible to ordinary calls. While the
// reconnect's initialize is suspended, a normal RPC must not reach the server; once recovery
// finishes it must, and only then. This is the timing a sessionGate unit test cannot show.
func TestReconnectSessionBarrierHoldsNormalCallsUntilRecovered(t *testing.T) {
	client, release, methods := suspendReconnect(t)

	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.ThreadLoadedList(context.Background())
		rpcDone <- err
	}()

	// The gate is armed before the server even sees the reconnect handshake, so the call must
	// not reach the server while initialize is suspended.
	select {
	case err := <-rpcDone:
		t.Fatalf("a normal RPC completed while the session was suspended (err=%v)", err)
	case <-time.After(200 * time.Millisecond):
	}

	release()

	var seen []string
	deadline := time.After(10 * time.Second)
collect:
	for {
		select {
		case m := <-methods:
			seen = append(seen, m)
			if m == barrierNormalMethod {
				break collect
			}
		case <-deadline:
			t.Fatalf("the normal RPC never reached the server; saw %v", seen)
		}
	}

	if err := <-rpcDone; err != nil {
		t.Fatalf("normal RPC after recovery: %v", err)
	}
	// Recovery completed before the ordinary call reached the server. (The helper already
	// consumed the reconnect's `initialize`, which arrived and blocked first.)
	assertSeenBefore(t, seen, "thread/resume", barrierNormalMethod)
	if !contains(seen, "initialized") {
		t.Fatalf("recovery did not send the initialized notification; saw %v", seen)
	}
}

// A gated call must still honour its own context: it is cancelled, and it never reaches the
// server, because recovery itself does not deadlock on the barrier.
func TestReconnectSessionBarrierHonoursContext(t *testing.T) {
	client, release, methods := suspendReconnect(t)
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.ThreadLoadedList(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gated call error = %v, want context.DeadlineExceeded", err)
	}

	// The cancelled call must not have reached the server: with initialize still suspended,
	// nothing but the handshake is outstanding.
	select {
	case m := <-methods:
		if m == barrierNormalMethod {
			t.Fatal("a cancelled gated call reached the server")
		}
	default:
	}
}
