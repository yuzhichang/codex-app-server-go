package codexgo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// A minimal app-server: replies to requests, records what it was asked, and can drop the
// connection on demand.
type fakeAppServer struct {
	mu      sync.Mutex
	methods []string
	conn    *websocket.Conn
	conns   int

	// Test hooks, all nil in the default harness.
	//
	// blockInit, when non-nil, makes the handler hold the `initialize` reply until it is
	// closed -- so a test can suspend re-initialization and observe what the client does
	// meanwhile. initBlocked is closed once initialize actually starts blocking. onMethod
	// receives every recorded method in order (buffered; drops if the test is not reading).
	blockInit   chan struct{}
	initBlocked chan struct{}
	onMethod    chan string
}

func (s *fakeAppServer) record(method string) {
	s.mu.Lock()
	s.methods = append(s.methods, method)
	s.mu.Unlock()
}

func (s *fakeAppServer) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

func (s *fakeAppServer) reset() {
	s.mu.Lock()
	s.methods = nil
	s.mu.Unlock()
}

// setBlockInit installs (or clears) the initialize-reply suspension. Set it only after the
// initial handshake has completed, or the very first initialize would block too.
func (s *fakeAppServer) setBlockInit(ch chan struct{}) {
	s.mu.Lock()
	s.blockInit = ch
	s.mu.Unlock()
}

func (s *fakeAppServer) handler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()

		s.mu.Lock()
		s.conns++
		s.conn = c
		s.mu.Unlock()

		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.Method == "" {
				continue // a reply to a server-initiated request
			}
			s.record(msg.Method)

			s.mu.Lock()
			onMethod, blockInit, initBlocked := s.onMethod, s.blockInit, s.initBlocked
			s.mu.Unlock()
			if onMethod != nil {
				select {
				case onMethod <- msg.Method:
				default:
				}
			}
			if msg.Method == "initialize" && blockInit != nil {
				if initBlocked != nil {
					select {
					case <-initBlocked:
					default:
						close(initBlocked)
					}
				}
				select {
				case <-blockInit:
				case <-ctx.Done():
					return
				}
			}

			if len(msg.ID) == 0 {
				continue // notification: no reply
			}

			var result string
			switch msg.Method {
			case "thread/start", "thread/resume":
				// Both must return a thread object: a start reply without an id leaves the
				// SDK with an unnamed thread, which cannot be resumed after a reconnect.
				result = `{"thread":{"id":"t1"}}`
			default:
				result = `{}`
			}
			reply := fmt.Sprintf(`{"id":%s,"result":%s}`, msg.ID, result)
			if err := c.Write(ctx, websocket.MessageText, []byte(reply)); err != nil {
				return
			}
		}
	}
}

// drop severs the live connection without a close handshake, so the client sees an
// abnormal closure rather than a negotiated shutdown.
func (s *fakeAppServer) drop() {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		_ = c.Close(websocket.StatusAbnormalClosure, "test drop")
	}
}

// The exported way to build a reconnecting transport must actually satisfy
// WithAutoReconnect. It did not: New() wrapped the transport in an adapter that drops
// Reconnects(), so the capability check failed and the documented combination was
// impossible to construct. Unit tests missed it because they passed a transport straight
// through WithTransport, bypassing the wrapper.
func TestAutoReconnectWorksWithExportedWSTransport(t *testing.T) {
	server := &fakeAppServer{}
	srv := httptest.NewServer(server.handler())
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, err := codexgo.New(
		codexgo.WithReconnectingWSTransport(context.Background(), url),
		codexgo.WithAutoReconnect(),
	)
	if err != nil {
		t.Fatalf("WithAutoReconnect must work with the exported WS transport: %v", err)
	}
	defer client.Close()

	// The handshake ran, so the connection and the supervisor are both live.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	thread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	defer thread.Close()

	sub := client.Events()
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	server.reset()

	// Drop the connection. The transport re-dials, and the supervisor must replay the
	// handshake and resume the open thread -- the server sees initialize and thread/resume
	// again on the new connection.
	server.drop()

	deadline := time.After(10 * time.Second)
	failed := false
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev.Method == codexgo.EventMethodReconnectFailed {
				failed = true
			}
			if ev.Method != codexgo.EventMethodReconnectSucceeded {
				continue
			}
		case <-deadline:
			t.Fatal("no recovery after the connection was dropped")
		}
		break
	}
	if failed {
		t.Fatal("recovery reported a failure; the fake server accepts every re-dial")
	}

	calls := server.called()
	for _, want := range []string{"initialize", "thread/resume"} {
		if !contains(calls, want) {
			t.Errorf("recovery did not replay %q on the new connection; server saw %v", want, calls)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
