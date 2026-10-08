// Package codexgo_test contains integration tests for the codex-go SDK that
// use a lightweight mock JSON-RPC 2.0 server (no real codex binary required).
// These tests exercise the full client↔transport↔server stack via real net.Pipe
// connections but stay entirely in-process.
//
// The mock server writes plain newline-delimited JSON (channel.Line compatible)
// and the client uses codexgo.NewStdioTransport (jrpc2 + channel.Line), so they
// speak the same framing discipline.
package codexgo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
	"github.com/zealbase/codex-app-server-go/internal/testutil"
	"github.com/zealbase/codex-app-server-go/internal/transport"
)

// pipeConn adapts a net.Conn to io.ReadCloser / io.WriteCloser.
// net.Conn already implements both, but we need separate rc/wc for
// codexgo.NewStdioTransport which takes (io.ReadCloser, io.WriteCloser).
type halfConn struct {
	net.Conn
}

// newClientFromMock builds a pair of net.Pipe conns, wires the server-side into
// mock, and returns a fully initialised codexgo.Client + the mock.
// Both are cleaned up via t.Cleanup.
func newClientFromMock(t *testing.T, opts ...codexgo.Option) (*codexgo.Client, *testutil.MockServer) {
	t.Helper()

	mock, clientConn := testutil.NewMockServer()

	// Register no-op "initialized" notification handler.
	mock.Handle("initialized", func(_ json.RawMessage) (any, error) {
		return nil, nil
	})

	// Split clientConn (net.Conn) into separate read/write closers for NewStdioTransport.
	pr, pw := io.Pipe()
	pr2, pw2 := io.Pipe()

	// clientConn → pw2 (we read from clientConn, write to pw2 which feeds pr2 as stdout)
	// But actually we need to bridge net.Conn I/O through io.Pipe properly.
	// Simpler: use net.Pipe directly and wrap net.Conn as rc/wc.
	// net.Conn implements both io.Reader and io.Writer. We just need ReadCloser + WriteCloser.
	// Use two wrappers pointing at the same underlying conn.
	_ = pr
	_ = pw
	_ = pr2
	_ = pw2

	// Wrap the same net.Conn as both stdin (read end) and stdout (write end).
	rc := &connReadCloser{clientConn}
	wc := &connWriteCloser{clientConn}

	baseOpts := []codexgo.Option{codexgo.WithStdioTransport(rc, wc)}
	baseOpts = append(baseOpts, opts...)

	client, err := codexgo.New(baseOpts...)
	if err != nil {
		_ = clientConn.Close()
		mock.Close()
		t.Fatalf("codexgo.New(): %v", err)
	}

	t.Cleanup(func() {
		client.Close()
		mock.Close()
	})
	return client, mock
}

// connReadCloser wraps net.Conn as io.ReadCloser.
type connReadCloser struct{ net.Conn }

func (c *connReadCloser) Read(p []byte) (int, error) { return c.Conn.Read(p) }
func (c *connReadCloser) Close() error               { return c.Conn.Close() }

// connWriteCloser wraps net.Conn as io.WriteCloser.
type connWriteCloser struct{ net.Conn }

func (c *connWriteCloser) Write(p []byte) (int, error) { return c.Conn.Write(p) }
func (c *connWriteCloser) Close() error                { return c.Conn.Close() }

// ---- Integration: Initialize ----

func TestMockServerInitialize(t *testing.T) {
	mock, clientConn := testutil.NewMockServer()
	defer mock.Close()

	mock.Handle("initialize", func(params json.RawMessage) (any, error) {
		var req codexgo.InitializeParams
		testutil.MustReadParams(params, &req)
		return map[string]any{
			"userAgent":      "mock-server/1.0",
			"platformFamily": "linux",
		}, nil
	})
	mock.Handle("initialized", func(_ json.RawMessage) (any, error) {
		return nil, nil
	})

	rc := &connReadCloser{clientConn}
	wc := &connWriteCloser{clientConn}
	client, err := codexgo.New(codexgo.WithStdioTransport(rc, wc))
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := client.Initialize(ctx, codexgo.InitializeParams{
		ClientInfo: codexgo.ClientInfo{
			Name:    "test-sdk",
			Version: "0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	if result.UserAgent != "mock-server/1.0" {
		t.Fatalf("unexpected UserAgent: %q", result.UserAgent)
	}
	if result.PlatformFamily != "linux" {
		t.Fatalf("unexpected PlatformFamily: %q", result.PlatformFamily)
	}
}

// ---- Integration: ThreadStart ----

func TestMockServerThreadStart(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(params json.RawMessage) (any, error) {
		var req codexgo.ThreadStartParams
		testutil.MustReadParams(params, &req)
		return map[string]any{
			"thread": map[string]any{
				"id":     "thread-abc123",
				"status": "idle",
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := client.ThreadStart(ctx, codexgo.ThreadStartParams{
		Model:          "gpt-5.1",
		ApprovalPolicy: "on-request",
	})
	if err != nil {
		t.Fatalf("ThreadStart(): %v", err)
	}
	if thread.ID != "thread-abc123" {
		t.Fatalf("unexpected thread ID: %q", thread.ID)
	}
}

// ---- Integration: ThreadResume ----

func TestMockServerThreadResume(t *testing.T) {
	client, mock := newClientFromMock(t)

	var receivedThreadID string
	mock.Handle("thread/resume", func(params json.RawMessage) (any, error) {
		var req struct {
			ThreadID string `json:"threadId"`
		}
		testutil.MustReadParams(params, &req)
		receivedThreadID = req.ThreadID
		return map[string]any{
			"thread": map[string]any{
				"id":     req.ThreadID,
				"status": "idle",
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := client.ThreadResume(ctx, codexgo.ThreadResumeParams{ThreadID: "thread-xyz"})
	if err != nil {
		t.Fatalf("ThreadResume(): %v", err)
	}
	if thread.ID != "thread-xyz" {
		t.Fatalf("unexpected thread ID: %q", thread.ID)
	}
	if receivedThreadID != "thread-xyz" {
		t.Fatalf("server did not receive correct threadId: %q", receivedThreadID)
	}
}

// ---- Integration: ThreadRead ----

func TestMockServerThreadRead(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/read", func(params json.RawMessage) (any, error) {
		var req struct {
			ThreadID     string `json:"threadId"`
			IncludeTurns bool   `json:"includeTurns"`
		}
		testutil.MustReadParams(params, &req)
		thread := map[string]any{
			"id":     req.ThreadID,
			"status": "idle",
		}
		if req.IncludeTurns {
			thread["turns"] = []any{
				map[string]any{"id": "turn-1", "status": "completed"},
			}
		}
		return map[string]any{"thread": thread}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	thread, err := client.ThreadRead(ctx, codexgo.ThreadReadParams{
		ThreadID:     "thread-read-test",
		IncludeTurns: true,
	})
	if err != nil {
		t.Fatalf("ThreadRead(): %v", err)
	}
	if thread.ID != "thread-read-test" {
		t.Fatalf("unexpected thread ID: %q", thread.ID)
	}
	if len(thread.Turns) != 1 || thread.Turns[0].ID != "turn-1" {
		t.Fatalf("unexpected turns: %+v", thread.Turns)
	}
}

// ---- Integration: TurnStart ----

func TestMockServerTurnStart(t *testing.T) {
	client, mock := newClientFromMock(t)

	var capturedText string
	mock.Handle("turn/start", func(params json.RawMessage) (any, error) {
		// TurnStartParams.MarshalJSON converts Input string to
		// [{"type":"text","text":"..."}] per the server protocol.
		var wire struct {
			ThreadID string `json:"threadId"`
			Input    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"input"`
		}
		testutil.MustReadParams(params, &wire)
		if len(wire.Input) > 0 {
			capturedText = wire.Input[0].Text
		}
		return map[string]any{
			"turn": map[string]any{
				"id":     "turn-start-1",
				"status": "inProgress",
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	turn, err := client.TurnStart(ctx, codexgo.TurnStartParams{
		ThreadID: "thread-1",
		Input:    "hello world",
	})
	if err != nil {
		t.Fatalf("TurnStart(): %v", err)
	}
	if turn.ID != "turn-start-1" {
		t.Fatalf("unexpected turn ID: %q", turn.ID)
	}
	if capturedText != "hello world" {
		t.Fatalf("server did not receive correct input text: %q", capturedText)
	}
}

// ---- Integration: TurnInterrupt ----

func TestMockServerTurnInterrupt(t *testing.T) {
	client, mock := newClientFromMock(t)

	interrupted := make(chan struct{}, 1)
	mock.Handle("turn/interrupt", func(params json.RawMessage) (any, error) {
		select {
		case interrupted <- struct{}{}:
		default:
		}
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.TurnInterrupt(ctx, codexgo.TurnInterruptParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	}); err != nil {
		t.Fatalf("TurnInterrupt(): %v", err)
	}

	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("timeout: server did not receive TurnInterrupt")
	}
}

// ---- Integration: SetModel ----

// configWriteRequest mirrors the params the setters must produce.
type configWriteRequest struct {
	KeyPath         string          `json:"keyPath"`
	Value           json.RawMessage `json:"value"`
	MergeStrategy   string          `json:"mergeStrategy"`
	ExpectedVersion string          `json:"expectedVersion"`
}

// newConfigWriteMock stubs config/read (reporting version "v7" for every key) and captures
// the config/value/write params the setters issue.
func newConfigWriteMock(t *testing.T, mock *testutil.MockServer) <-chan configWriteRequest {
	t.Helper()
	mock.Handle("config/read", func(json.RawMessage) (any, error) {
		return map[string]any{
			"config": map[string]any{"model": "old-model"},
			"origins": map[string]any{
				"model":           map[string]any{"name": "user", "version": "v7"},
				"approval_policy": map[string]any{"name": "user", "version": "v7"},
				"sandbox_mode":    map[string]any{"name": "user", "version": "v7"},
			},
		}, nil
	})
	got := make(chan configWriteRequest, 4)
	mock.Handle("config/value/write", func(params json.RawMessage) (any, error) {
		var req configWriteRequest
		testutil.MustReadParams(params, &req)
		got <- req
		return map[string]any{"filePath": "/tmp/config.toml", "status": "ok"}, nil
	})
	return got
}

// The setters used to call the removed `config/update` RPC. They now write a single config
// key via config/value/write, carrying the version read from config/read as expectedVersion
// so a concurrent external edit fails the write rather than being overwritten.
func TestMockServerSetModel(t *testing.T) {
	client, mock := newClientFromMock(t)
	got := newConfigWriteMock(t, mock)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.SetModel(ctx, "gpt-5.1"); err != nil {
		t.Fatalf("SetModel(): %v", err)
	}

	select {
	case req := <-got:
		if req.KeyPath != "model" {
			t.Errorf("keyPath = %q, want model", req.KeyPath)
		}
		if string(req.Value) != `"gpt-5.1"` {
			t.Errorf("value = %s, want a JSON string", req.Value)
		}
		if req.MergeStrategy != string(codexgo.MergeStrategyReplace) {
			t.Errorf("mergeStrategy = %q, want replace", req.MergeStrategy)
		}
		if req.ExpectedVersion != "v7" {
			t.Errorf("expectedVersion = %q, want v7 (read from config/read origins)", req.ExpectedVersion)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for config/value/write")
	}
}

// The config key for each setter must be the upstream Config key (snake_case), not the
// camelCase RPC param name of the same concept. SetSandbox is the one that regressed: the
// removed config/update payload used `sandboxPolicy`, which is not a Config key at all.
func TestMockServerSetterConfigKeys(t *testing.T) {
	cases := []struct {
		name    string
		keyPath string
		call    func(context.Context, *codexgo.Client) error
	}{
		{"SetApprovalPolicy", "approval_policy", func(ctx context.Context, c *codexgo.Client) error {
			return c.SetApprovalPolicy(ctx, "on-failure")
		}},
		{"SetSandbox", "sandbox_mode", func(ctx context.Context, c *codexgo.Client) error {
			return c.SetSandbox(ctx, "workspace-write")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, mock := newClientFromMock(t)
			got := newConfigWriteMock(t, mock)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if err := tc.call(ctx, client); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			select {
			case req := <-got:
				if req.KeyPath != tc.keyPath {
					t.Fatalf("keyPath = %q, want %q", req.KeyPath, tc.keyPath)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for config/value/write")
			}
		})
	}
}

// ---- Integration: Server-pushed notifications → Events() ----

func TestMockServerPushNotificationToClient(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	// Give transports time to start.
	time.Sleep(20 * time.Millisecond)

	// Push a turn/started notification from the mock server.
	if err := mock.Notify("turn/started", map[string]any{
		"threadId": "t1",
		"turnId":   "turn-a",
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != "turn/started" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.TurnStartedEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want TurnStartedEvent", event.Value)
		}
		if typed.ThreadID != "t1" || typed.TurnID != "turn-a" {
			t.Fatalf("unexpected event payload: %+v", typed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for turn/started notification")
	}
}

// ---- Integration: Server-pushed turn/completed notification ----

func TestMockServerTurnCompletedNotification(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	if err := mock.Notify("turn/completed", map[string]any{
		"threadId": "t2",
		"turnId":   "turn-b",
		"status":   "completed",
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != "turn/completed" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.TurnCompletedEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want TurnCompletedEvent", event.Value)
		}
		if typed.Status != codexgo.TurnStatusCompleted {
			t.Fatalf("unexpected status: %q", typed.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for turn/completed notification")
	}
}

// ---- Integration: Server sends item/started + item/completed ----

func TestMockServerItemLifecycleNotifications(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	_ = mock.Notify("item/started", map[string]any{
		"threadId": "t3",
		"turnId":   "turn-c",
		"item":     map[string]any{"id": "item-1", "type": "agentMessage"},
	})
	_ = mock.Notify("item/completed", map[string]any{
		"threadId": "t3",
		"turnId":   "turn-c",
		"item":     map[string]any{"id": "item-1", "type": "agentMessage"},
	})

	got := make(map[string]bool)
	deadline := time.After(3 * time.Second)
	for {
		allReceived := true
		for _, v := range got {
			if !v {
				allReceived = false
				break
			}
		}
		if got["item/started"] && got["item/completed"] && allReceived {
			return // success
		}
		select {
		case event := <-sub.C():
			got[event.Method] = true
		case <-deadline:
			t.Fatalf("timeout; received: %v", got)
		}
	}
}

// ---- Integration: Inbound server-request (approval) via Dispatcher ----

func TestMockServerDispatcherExecApproval(t *testing.T) {
	approved := make(chan string, 1)

	dispatcher := &codexgo.Dispatcher{
		Exec: execApprovalHandlerFunc(func(_ context.Context, req codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
			approved <- req.Command
			return codexgo.CommandExecutionApprovalResult{Decision: codexgo.CommandExecutionApprovalDecisionAccept}, nil
		}),
	}

	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))

	// Give the request loop time to start.
	time.Sleep(20 * time.Millisecond)

	// Send an inbound request from the mock server.
	go func() {
		_, _ = mock.Request(
			context.Background(),
			88,
			"item/commandExecution/requestApproval",
			map[string]any{"command": "echo hello", "threadId": "t5"},
		)
	}()

	select {
	case cmd := <-approved:
		if cmd != "echo hello" {
			t.Fatalf("unexpected command: %q", cmd)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for exec approval")
	}
}

// execApprovalHandlerFunc adapts a function to ExecApprovalHandler.
type execApprovalHandlerFunc func(context.Context, codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error)

func (f execApprovalHandlerFunc) HandleCommandExecutionApproval(ctx context.Context, req codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
	return f(ctx, req)
}

// ---- Integration: WaitForTurn with mock server polling + notification ----

func TestMockServerWaitForTurn(t *testing.T) {
	client, mock := newClientFromMock(t)

	var mu sync.Mutex
	turnCompleted := false

	mock.Handle("thread/read", func(params json.RawMessage) (any, error) {
		mu.Lock()
		done := turnCompleted
		mu.Unlock()

		status := "inProgress"
		if done {
			status = "completed"
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-wft",
				"turns": []any{
					map[string]any{"id": "turn-wft", "status": status},
				},
			},
		}, nil
	})

	// After a brief delay, mark the turn complete and push a notification.
	go func() {
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		turnCompleted = true
		mu.Unlock()
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-wft",
			"turnId":   "turn-wft",
			"status":   "completed",
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	turn, err := client.WaitForTurn(ctx, "thread-wft", "turn-wft")
	if err != nil {
		t.Fatalf("WaitForTurn(): %v", err)
	}
	if turn.ID != "turn-wft" {
		t.Fatalf("unexpected turn ID: %q", turn.ID)
	}
	if turn.Status != codexgo.TurnStatusCompleted {
		t.Fatalf("unexpected turn status: %q", turn.Status)
	}
}

// ---- Integration: StartThread + Run lifecycle via SessionThread ----

func TestMockServerSessionThreadRun(t *testing.T) {
	client, mock := newClientFromMock(t)

	var mu sync.Mutex
	turnCompleted := false

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "session-thread-1", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"turn": map[string]any{"id": "session-turn-1", "status": "inProgress"},
		}, nil
	})
	mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
		mu.Lock()
		done := turnCompleted
		mu.Unlock()

		status := "inProgress"
		if done {
			status = "completed"
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "session-thread-1",
				"turns": []any{
					map[string]any{
						"id":     "session-turn-1",
						"status": status,
					},
				},
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionThread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	if sessionThread.ID() != "session-thread-1" {
		t.Fatalf("unexpected thread ID: %q", sessionThread.ID())
	}

	// Fire completion notification after a delay.
	go func() {
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		turnCompleted = true
		mu.Unlock()
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "session-thread-1",
			"turnId":   "session-turn-1",
			"status":   "completed",
		})
	}()

	result, err := sessionThread.Run(ctx, "ping")
	if err != nil {
		t.Fatalf("SessionThread.Run(): %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil TurnResult")
	}
	if result.Turn.ID != "session-turn-1" {
		t.Fatalf("unexpected turn ID in result: %q", result.Turn.ID)
	}
}

// ---- Integration: ResumeThread ----

func TestMockServerResumeThread(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/resume", func(params json.RawMessage) (any, error) {
		var req struct {
			ThreadID string `json:"threadId"`
		}
		testutil.MustReadParams(params, &req)
		return map[string]any{
			"thread": map[string]any{"id": req.ThreadID, "status": "idle"},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := client.ResumeThread(ctx, "existing-thread-id")
	if err != nil {
		t.Fatalf("ResumeThread(): %v", err)
	}
	if st.ID() != "existing-thread-id" {
		t.Fatalf("unexpected thread ID: %q", st.ID())
	}
}

// ---- Integration: Thread Compact ----

func TestMockServerThreadCompact(t *testing.T) {
	client, mock := newClientFromMock(t)

	compacted := make(chan string, 1)
	mock.Handle("thread/compact/start", func(params json.RawMessage) (any, error) {
		var req struct {
			ThreadID string `json:"threadId"`
		}
		testutil.MustReadParams(params, &req)
		select {
		case compacted <- req.ThreadID:
		default:
		}
		return nil, nil
	})
	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "compact-thread-1", "status": "idle"},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}

	if err := st.Compact(ctx); err != nil {
		t.Fatalf("Compact(): %v", err)
	}

	select {
	case tid := <-compacted:
		if tid != "compact-thread-1" {
			t.Fatalf("unexpected thread ID in compact request: %q", tid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread/compact")
	}
}

// ---- Integration: Concurrent calls on the same client ----

func TestMockServerConcurrentThreadStarts(t *testing.T) {
	client, mock := newClientFromMock(t)

	var mu sync.Mutex
	count := 0

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		mu.Lock()
		count++
		mu.Unlock()
		return map[string]any{
			"thread": map[string]any{"id": "thread-concurrent", "status": "idle"},
		}, nil
	})

	const n = 5
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := client.ThreadStart(ctx, codexgo.ThreadStartParams{})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ThreadStart error: %v", err)
	}

	mu.Lock()
	if count != n {
		t.Errorf("expected %d thread/start calls, got %d", n, count)
	}
	mu.Unlock()
}

// ---- Integration: Multiple event types dispatched correctly ----

func TestMockServerMultipleEventTypes(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	notifications := []struct {
		method string
		params map[string]any
	}{
		{
			"turn/started",
			map[string]any{"threadId": "t10", "turnId": "turn-10"},
		},
		{
			"item/agentMessage/delta",
			map[string]any{"threadId": "t10", "turnId": "turn-10", "text": "hello"},
		},
		{
			"turn/completed",
			map[string]any{"threadId": "t10", "turnId": "turn-10", "status": "completed"},
		},
	}

	for _, n := range notifications {
		if err := mock.Notify(n.method, n.params); err != nil {
			t.Fatalf("mock.Notify(%q): %v", n.method, err)
		}
	}

	wantMethods := map[string]bool{
		"turn/started":            false,
		"item/agentMessage/delta": false,
		"turn/completed":          false,
	}
	deadline := time.After(3 * time.Second)
	for {
		allReceived := true
		for _, v := range wantMethods {
			if !v {
				allReceived = false
				break
			}
		}
		if allReceived {
			return
		}
		select {
		case event := <-sub.C():
			if _, ok := wantMethods[event.Method]; ok {
				wantMethods[event.Method] = true
			}
		case <-deadline:
			t.Fatalf("timeout; not all events received: %v", wantMethods)
		}
	}
}

// ---- Integration: error event dispatching ----

func TestMockServerErrorEventDispatched(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	_ = mock.Notify("error", map[string]any{
		"threadId": "t20",
		"turnId":   "turn-20",
		"message":  "something went wrong",
		"code":     "internal_error",
	})

	select {
	case event := <-sub.C():
		if event.Method != "error" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.ErrorEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want ErrorEvent", event.Value)
		}
		if typed.Message != "something went wrong" {
			t.Fatalf("unexpected error message: %q", typed.Message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for error event")
	}
}

// ---- Integration: thread/tokenUsage/updated event ----

func TestMockServerThreadTokenUsageEvent(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	// Upstream shape: {threadId, turnId, tokenUsage:{last,total,modelContextWindow}}.
	// The flat `usage` document this test used to send is not what the server emits.
	_ = mock.Notify("thread/tokenUsage/updated", map[string]any{
		"threadId": "t30",
		"turnId":   "turn-30",
		"tokenUsage": map[string]any{
			"last": map[string]any{
				"cachedInputTokens":     100,
				"inputTokens":           1000,
				"outputTokens":          200,
				"reasoningOutputTokens": 50,
				"totalTokens":           1200,
			},
			"total": map[string]any{
				"cachedInputTokens":     400,
				"inputTokens":           4000,
				"outputTokens":          800,
				"reasoningOutputTokens": 200,
				"totalTokens":           4800,
			},
			"modelContextWindow": 128000,
		},
	})

	select {
	case event := <-sub.C():
		if event.Method != "thread/tokenUsage/updated" {
			t.Fatalf("unexpected method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.ThreadTokenUsageUpdatedEvent)
		if !ok {
			t.Fatalf("event.Value type = %T", event.Value)
		}
		if typed.TurnID != "turn-30" {
			t.Errorf("turnId = %q, want turn-30", typed.TurnID)
		}
		if typed.TokenUsage == nil {
			t.Fatal("tokenUsage was not decoded")
		}
		if typed.TokenUsage.Last.InputTokens != 1000 {
			t.Errorf("last.inputTokens = %d, want 1000", typed.TokenUsage.Last.InputTokens)
		}
		if typed.TokenUsage.Total.TotalTokens != 4800 {
			t.Errorf("total.totalTokens = %d, want 4800", typed.TokenUsage.Total.TotalTokens)
		}
		if typed.TokenUsage.ModelContextWindow != 128000 {
			t.Errorf("modelContextWindow = %d, want 128000", typed.TokenUsage.ModelContextWindow)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for token usage event")
	}
}

// ---- F6a: TestTurnFailedPopulatesError ----

func TestTurnFailedPopulatesError(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-failed-1", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"turn": map[string]any{"id": "turn-failed-1", "status": "inProgress"},
		}, nil
	})
	mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-failed-1",
				"turns": []any{
					map[string]any{
						"id":     "turn-failed-1",
						"status": "failed",
						"error":  map[string]any{"message": "context limit exceeded"},
					},
				},
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionThread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer sessionThread.Close()

	// Push turn/completed with failed status after a brief delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-failed-1",
			"turnId":   "turn-failed-1",
			"status":   "failed",
		})
	}()

	result, err := sessionThread.Run(ctx, "trigger failure")
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil TurnResult")
	}
	if result.Turn.Status != codexgo.TurnStatusFailed {
		t.Fatalf("expected TurnStatusFailed, got %q", result.Turn.Status)
	}
	if result.Error == nil {
		t.Fatal("expected TurnResult.Error to be non-nil for failed turn")
	}
	if result.Error.Message != "context limit exceeded" {
		t.Fatalf("unexpected error message: %q", result.Error.Message)
	}
}

// ---- F6b: TestIsRateLimited ----

func TestIsRateLimited(t *testing.T) {
	rpcErr := &transport.RPCError{
		Code:    -32000,
		Message: "rate limited",
		Data:    json.RawMessage(`{"codexErrorInfo":{"type":"HttpConnectionFailed","httpStatusCode":429}}`),
	}

	if !codexgo.IsRateLimited(rpcErr) {
		t.Fatal("expected IsRateLimited to return true for HTTP 429")
	}
	if codexgo.IsUnauthorized(rpcErr) {
		t.Fatal("expected IsUnauthorized to return false for HTTP 429")
	}
	if codexgo.IsInternalServerError(rpcErr) {
		t.Fatal("expected IsInternalServerError to return false for HTTP 429")
	}
	if !codexgo.IsHttpConnectionFailed(rpcErr) {
		t.Fatal("expected IsHttpConnectionFailed to return true for type HttpConnectionFailed")
	}

	// Verify AsCodexError extracts fields correctly.
	ce, ok := codexgo.AsCodexError(rpcErr)
	if !ok {
		t.Fatal("AsCodexError should succeed for transport.RPCError")
	}
	if ce.RPCCode != -32000 {
		t.Fatalf("unexpected RPCCode: %d", ce.RPCCode)
	}
	if ce.HTTPStatusCode != 429 {
		t.Fatalf("unexpected HTTPStatusCode: %d", ce.HTTPStatusCode)
	}
	if ce.ErrorType != "HttpConnectionFailed" {
		t.Fatalf("unexpected ErrorType: %q", ce.ErrorType)
	}
}

func TestIsUnauthorized(t *testing.T) {
	rpcErr := &transport.RPCError{
		Code:    -32001,
		Message: "unauthorized",
		Data:    json.RawMessage(`{"codexErrorInfo":{"type":"AuthError","httpStatusCode":401}}`),
	}
	if !codexgo.IsUnauthorized(rpcErr) {
		t.Fatal("expected IsUnauthorized to return true for HTTP 401")
	}
	if codexgo.IsRateLimited(rpcErr) {
		t.Fatal("expected IsRateLimited to return false for HTTP 401")
	}
}

// ---- F6c: TestMaxThreadsSemaphore ----

func TestMaxThreadsSemaphore(t *testing.T) {
	client, mock := newClientFromMock(t, codexgo.WithMaxThreads(2))

	var threadIDMu sync.Mutex
	threadIDCounter := 0

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		threadIDMu.Lock()
		threadIDCounter++
		id := threadIDCounter
		threadIDMu.Unlock()
		return map[string]any{
			"thread": map[string]any{
				"id":     "thread-sem-" + string(rune('0'+id)),
				"status": "idle",
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start 2 threads — should succeed.
	t1, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread 1: %v", err)
	}
	t2, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread 2: %v", err)
	}

	// Try to start a 3rd with a very short timeout — should fail.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()

	_, err = client.StartThread(shortCtx)
	if err == nil {
		t.Fatal("expected error starting 3rd thread when semaphore is full")
	}

	// Close one thread to release a slot.
	t1.Close()

	// Now starting a new one should succeed.
	t3, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread after Close: %v", err)
	}

	// Cleanup.
	t2.Close()
	t3.Close()
}

// ---- F6d: TestConcurrentRunStreamedOnSameThread ----

func TestConcurrentRunStreamedOnSameThread(t *testing.T) {
	client, mock := newClientFromMock(t)

	var (
		turnMu      sync.Mutex
		turnCounter int
	)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-concurrent-streamed", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		turnMu.Lock()
		turnCounter++
		id := turnCounter
		turnMu.Unlock()
		return map[string]any{
			"turn": map[string]any{
				"id":     "turn-concurrent-" + string(rune('0'+id)),
				"status": "inProgress",
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionThread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer sessionThread.Close()

	// Channel to track order of completion.
	order := make(chan int, 2)

	// Launch first RunStreamed. It should acquire the lock.
	ch1, err := sessionThread.RunStreamed(ctx, "first")
	if err != nil {
		t.Fatalf("RunStreamed 1: %v", err)
	}

	// Signal first turn as completed after a brief delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-concurrent-streamed",
			"turnId":   "turn-concurrent-1",
			"status":   "completed",
		})
	}()

	// Drain first channel in background; record completion.
	go func() {
		for range ch1 {
		}
		order <- 1
	}()

	// Wait briefly to ensure first RunStreamed has the lock, then try second.
	time.Sleep(20 * time.Millisecond)

	// This goroutine blocks on turnMu until first is done.
	go func() {
		ch2, err := sessionThread.RunStreamed(ctx, "second")
		if err != nil {
			// If blocked and ctx cancelled, that's fine for this test.
			order <- 2
			return
		}
		// Signal second turn complete immediately.
		go func() {
			time.Sleep(30 * time.Millisecond)
			_ = mock.Notify("turn/completed", map[string]any{
				"threadId": "thread-concurrent-streamed",
				"turnId":   "turn-concurrent-2",
				"status":   "completed",
			})
		}()
		for range ch2 {
		}
		order <- 2
	}()

	// Expect first to complete before second (serialized by turnMu).
	first := <-order
	if first != 1 {
		t.Fatalf("expected first turn to complete first, got %d", first)
	}
	// Second should eventually complete too.
	select {
	case <-order:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for second RunStreamed to complete")
	}
}

// ---- F6e: TestDynamicToolCallNoHandler ----

func TestDynamicToolCallNoHandler(t *testing.T) {
	// Dispatcher with no DynamicTool handler.
	dispatcher := &codexgo.Dispatcher{}

	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 42, "item/tool/call", map[string]any{
		"toolName": "my_tool",
		"input":    map[string]any{"arg": "value"},
		"threadId": "t-dyn",
		"turnId":   "turn-dyn",
		"itemId":   "item-dyn",
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	// Expect {"content":[]} response.
	var resp struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, string(result))
	}
	if len(resp.Content) != 0 {
		t.Fatalf("expected empty content array, got %d items", len(resp.Content))
	}
}

// ---- F6f: TestFileChangeApprovalDecline ----

func TestFileChangeApprovalDecline(t *testing.T) {
	dispatcher := &codexgo.Dispatcher{
		File: fileApprovalHandlerFunc(func(_ context.Context, _ codexgo.FileChangeApprovalRequest) (codexgo.FileChangeApprovalResult, error) {
			return codexgo.FileChangeApprovalResult{Decision: codexgo.FileChangeApprovalDecisionDecline}, nil
		}),
	}

	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 55, "item/fileChange/requestApproval", map[string]any{
		"itemId":   "item-fc",
		"threadId": "t-fc",
		"turnId":   "turn-fc",
		"reason":   "write to /etc/hosts",
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, string(result))
	}
	if resp.Decision != "decline" {
		t.Fatalf("expected decision=decline, got %q", resp.Decision)
	}
}

// fileApprovalHandlerFunc adapts a function to FileApprovalHandler.
type fileApprovalHandlerFunc func(context.Context, codexgo.FileChangeApprovalRequest) (codexgo.FileChangeApprovalResult, error)

func (f fileApprovalHandlerFunc) HandleFileChangeApproval(ctx context.Context, req codexgo.FileChangeApprovalRequest) (codexgo.FileChangeApprovalResult, error) {
	return f(ctx, req)
}

// ---- F6g: TestCommandExecApprovalBlocksTurn ----

func TestCommandExecApprovalBlocksTurn(t *testing.T) {
	dispatcher := &codexgo.Dispatcher{
		Exec: execApprovalHandlerFunc(func(_ context.Context, _ codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
			return codexgo.CommandExecutionApprovalResult{Decision: codexgo.CommandExecutionApprovalDecisionDecline}, nil
		}),
	}

	client, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-exec-block", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"turn": map[string]any{"id": "turn-exec-block", "status": "inProgress"},
		}, nil
	})
	mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-exec-block",
				"turns": []any{
					map[string]any{
						"id":     "turn-exec-block",
						"status": "interrupted",
					},
				},
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionThread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer sessionThread.Close()

	// In background: send exec approval request, wait for the decline response,
	// then push turn/completed with interrupted status.
	go func() {
		time.Sleep(30 * time.Millisecond)
		approvalCtx, approvalCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer approvalCancel()
		_, _ = mock.RequestAndWait(approvalCtx, 77, "item/commandExecution/requestApproval", map[string]any{
			"command":  "rm -rf /",
			"threadId": "thread-exec-block",
			"turnId":   "turn-exec-block",
		})
		// After decline, server sends interrupted turn.
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-exec-block",
			"turnId":   "turn-exec-block",
			"status":   "interrupted",
		})
	}()

	result, err := sessionThread.Run(ctx, "do dangerous thing")
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil TurnResult")
	}
	if result.Turn.Status != codexgo.TurnStatusInterrupted {
		t.Fatalf("expected TurnStatusInterrupted, got %q", result.Turn.Status)
	}
}

// ---- Test #6 — TestCommandExecutionOutputDeltaDecoded ----

func TestCommandExecutionOutputDeltaDecoded(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	if err := mock.Notify("item/commandExecution/outputDelta", map[string]any{
		"threadId": "t1",
		"turnId":   "r1",
		"itemId":   "i1",
		"stream":   "stdout",
		"delta":    "hello\n",
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != "item/commandExecution/outputDelta" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.ItemCommandExecutionOutputDeltaEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want ItemCommandExecutionOutputDeltaEvent", event.Value)
		}
		if typed.ThreadID != "t1" {
			t.Fatalf("unexpected threadId: %q", typed.ThreadID)
		}
		if typed.Stream != "stdout" {
			t.Fatalf("unexpected stream: %q", typed.Stream)
		}
		if typed.Delta != "hello\n" {
			t.Fatalf("unexpected delta: %q", typed.Delta)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for item/commandExecution/outputDelta notification")
	}
}

// ---- Test #7 — TestApprovalCancelVsDecline ----

func TestApprovalCancelVsDecline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision codexgo.CommandExecutionApprovalDecision
	}{
		{"cancel", codexgo.CommandExecutionApprovalDecisionCancel},
		{"decline", codexgo.CommandExecutionApprovalDecisionDecline},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dispatcher := &codexgo.Dispatcher{
				Exec: execApprovalHandlerFunc(func(_ context.Context, _ codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
					return codexgo.CommandExecutionApprovalResult{Decision: tc.decision}, nil
				}),
			}

			client, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))

			mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
				return map[string]any{
					"thread": map[string]any{"id": "thread-" + tc.name, "status": "idle"},
				}, nil
			})
			mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
				return map[string]any{
					"turn": map[string]any{"id": "turn-" + tc.name, "status": "inProgress"},
				}, nil
			})
			mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
				return map[string]any{
					"thread": map[string]any{
						"id": "thread-" + tc.name,
						"turns": []any{
							map[string]any{
								"id":     "turn-" + tc.name,
								"status": "interrupted",
							},
						},
					},
				}, nil
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			sessionThread, err := client.StartThread(ctx)
			if err != nil {
				t.Fatalf("StartThread(): %v", err)
			}
			defer sessionThread.Close()

			go func() {
				time.Sleep(30 * time.Millisecond)
				approvalCtx, approvalCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer approvalCancel()
				_, _ = mock.RequestAndWait(approvalCtx, 100, "item/commandExecution/requestApproval", map[string]any{
					"command":  "dangerous cmd",
					"threadId": "thread-" + tc.name,
					"turnId":   "turn-" + tc.name,
				})
				_ = mock.Notify("turn/completed", map[string]any{
					"threadId": "thread-" + tc.name,
					"turnId":   "turn-" + tc.name,
					"status":   "interrupted",
				})
			}()

			result, err := sessionThread.Run(ctx, "do something")
			if err != nil {
				t.Fatalf("Run() returned unexpected error for decision=%q: %v", tc.decision, err)
			}
			if result == nil {
				t.Fatal("expected non-nil TurnResult")
			}
			if result.Turn.Status != codexgo.TurnStatusInterrupted {
				t.Fatalf("expected TurnStatusInterrupted, got %q", result.Turn.Status)
			}
		})
	}
}

// ---- Test #8 — TestPermissionsApprovalHandler ----

type permissionsHandlerFunc func(context.Context, codexgo.PermissionsApprovalRequest) (codexgo.PermissionsApprovalResponse, error)

func (f permissionsHandlerFunc) HandlePermissionsApproval(ctx context.Context, req codexgo.PermissionsApprovalRequest) (codexgo.PermissionsApprovalResponse, error) {
	return f(ctx, req)
}

// The SDK answers `item/permissions/requestApproval` itself now, so a Fallback handler no
// longer intercepts it -- Dispatcher.Permissions is the supported way. This test previously
// installed a Fallback precisely because nothing handled the method.
func TestPermissionsApprovalHandler(t *testing.T) {
	const permissionsMethod = "item/permissions/requestApproval"

	var got codexgo.PermissionsApprovalRequest
	handler := permissionsHandlerFunc(func(_ context.Context, req codexgo.PermissionsApprovalRequest) (codexgo.PermissionsApprovalResponse, error) {
		got = req
		return codexgo.PermissionsApprovalResponse{
			Permissions: codexgo.GrantedPermissionProfile{Network: json.RawMessage(`{"enabled":true}`)},
		}, nil
	})

	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{Permissions: handler}))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 200, permissionsMethod, map[string]any{
		"itemId":   "item-perm",
		"threadId": "t-perm",
		"turnId":   "turn-perm",
		// Upstream sends an OBJECT here. The old SDK typed it as []string, which meant a
		// real request could not be decoded at all.
		"permissions": map[string]any{"fileSystem": map[string]any{"read": []string{"/tmp/x"}}},
		"cwd":         "/tmp",
		"startedAtMs": 1,
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	// The handler must receive the request with its structured profile intact.
	if got.ThreadID != "t-perm" || got.TurnID != "turn-perm" {
		t.Fatalf("handler got unexpected ids: %+v", got)
	}
	if len(got.Permissions) == 0 || got.Permissions[0] != '{' {
		t.Fatalf("permissions profile not preserved: %s", got.Permissions)
	}

	var resp codexgo.PermissionsApprovalResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, string(result))
	}
	if len(resp.Permissions.Network) == 0 {
		t.Fatalf("handler's grant was not relayed: %+v", resp.Permissions)
	}
}

// ---- Test #10 — TestStreamingTurnFailureEvent ----

func TestStreamingTurnFailureEvent(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-stream-fail", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"turn": map[string]any{"id": "turn-stream-fail", "status": "inProgress"},
		}, nil
	})
	mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-stream-fail",
				"turns": []any{
					map[string]any{
						"id":     "turn-stream-fail",
						"status": "failed",
						"error":  map[string]any{"message": "sandbox timeout"},
					},
				},
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessionThread, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer sessionThread.Close()

	ch, err := sessionThread.RunStreamed(ctx, "test prompt")
	if err != nil {
		t.Fatalf("RunStreamed(): %v", err)
	}

	// Push turn/started then turn/completed with failed status.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = mock.Notify("turn/started", map[string]any{
			"threadId": "thread-stream-fail",
			"turnId":   "turn-stream-fail",
		})
		time.Sleep(20 * time.Millisecond)
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-stream-fail",
			"turnId":   "turn-stream-fail",
			"status":   "failed",
			"error":    map[string]any{"message": "sandbox timeout"},
		})
	}()

	// Drain the streaming channel and collect events.
	var lastEvent codexgo.ThreadEvent
	for te := range ch {
		lastEvent = te
	}

	if lastEvent.Kind != "turn/completed" {
		t.Fatalf("expected last event Kind=turn/completed, got %q", lastEvent.Kind)
	}

	// Verify TurnResult from Run (not RunStreamed) shows the error.
	// Re-use a fresh session thread to call Run synchronously on the already-completed turn.
	// Instead, just verify the completed event carried status=failed by inspecting the raw value.
	tc, ok := lastEvent.Raw.(codexgo.TurnCompletedEvent)
	if !ok {
		t.Fatalf("last event Raw type = %T, want TurnCompletedEvent", lastEvent.Raw)
	}
	if tc.Status != codexgo.TurnStatusFailed {
		t.Fatalf("expected TurnStatusFailed in streaming event, got %q", tc.Status)
	}
}

// ---- TestConcurrentRunAndRunStreamed ----
// Two goroutines on the same SessionThread call Run and RunStreamed concurrently.
// They must not deadlock — the turn mutex serialises them so one completes before
// the other starts.

func TestConcurrentRunAndRunStreamed(t *testing.T) {
	client, mock := newClientFromMock(t)

	var (
		turnMu      sync.Mutex
		turnCounter int
	)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-run-mix", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		turnMu.Lock()
		turnCounter++
		id := turnCounter
		turnMu.Unlock()
		return map[string]any{
			"turn": map[string]any{
				"id":     "turn-mix-" + string(rune('0'+id)),
				"status": "inProgress",
			},
		}, nil
	})
	mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
		// Always report the latest turn as completed so Run returns.
		turnMu.Lock()
		cnt := turnCounter
		turnMu.Unlock()
		id := "turn-mix-" + string(rune('0'+cnt))
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-run-mix",
				"turns": []any{
					map[string]any{"id": id, "status": "completed"},
				},
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer st.Close()

	results := make(chan int, 2)

	// Goroutine 1: Run (synchronous).
	go func() {
		_, runErr := st.Run(ctx, "prompt-a")
		if runErr != nil && ctx.Err() == nil {
			t.Errorf("Run() error: %v", runErr)
		}
		results <- 1
	}()

	// Tiny delay to let Run acquire the lock first (best-effort ordering).
	time.Sleep(10 * time.Millisecond)

	// Goroutine 2: RunStreamed; signal completion after the channel is obtained.
	go func() {
		ch, rsErr := st.RunStreamed(ctx, "prompt-b")
		if rsErr != nil {
			if ctx.Err() != nil {
				results <- 2
				return
			}
			t.Errorf("RunStreamed() error: %v", rsErr)
			results <- 2
			return
		}
		// Trigger completion for this streamed turn.
		go func() {
			time.Sleep(20 * time.Millisecond)
			turnMu.Lock()
			cnt := turnCounter
			turnMu.Unlock()
			id := "turn-mix-" + string(rune('0'+cnt))
			_ = mock.Notify("turn/completed", map[string]any{
				"threadId": "thread-run-mix",
				"turnId":   id,
				"status":   "completed",
			})
		}()
		for range ch {
		}
		results <- 2
	}()

	// Signal Run's turn as completed after a brief delay.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-run-mix",
			"turnId":   "turn-mix-1",
			"status":   "completed",
		})
	}()

	// Both goroutines must complete without deadlock.
	got := 0
	for got < 2 {
		select {
		case <-results:
			got++
		case <-time.After(8 * time.Second):
			t.Fatal("timeout: concurrent Run + RunStreamed deadlocked")
		}
	}
}

// ---- TestGoroutineLeakOnCancel ----
// Start a RunStreamed, cancel context immediately, verify goroutine count
// returns to baseline (±5) within 200 ms.

func TestGoroutineLeakOnCancel(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-leak", "status": "idle"},
		}, nil
	})
	mock.Handle("turn/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"turn": map[string]any{"id": "turn-leak-1", "status": "inProgress"},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer st.Close()

	// Capture baseline after setup.
	runtime.GC()
	baseline := runtime.NumGoroutine()

	streamCtx, streamCancel := context.WithCancel(ctx)
	ch, err := st.RunStreamed(streamCtx, "leak-test")
	if err != nil {
		t.Fatalf("RunStreamed(): %v", err)
	}

	// Cancel immediately.
	streamCancel()

	// Drain channel so the goroutine can exit.
	go func() {
		for range ch {
		}
	}()

	// Wait for goroutine to exit.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		runtime.Gosched()
		if runtime.NumGoroutine() <= baseline+5 {
			return // pass
		}
		time.Sleep(10 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after > baseline+5 {
		t.Fatalf("goroutine leak: baseline=%d after cancel=%d (delta=%d > 5)", baseline, after, after-baseline)
	}
}

// ---- TestRateLimitRetry ----
// WaitForTurn polls thread/read multiple times before the turn appears as
// completed.  This exercises the transient-retry loop: the turn is absent (not
// yet found) for the first several polls, then appears as completed.  Both the
// poll path and the event-notification path converge on the completed result.

func TestRateLimitRetry(t *testing.T) {
	client, mock := newClientFromMock(t)

	var (
		readMu  sync.Mutex
		readCnt int
	)

	// First 4 reads return the thread with NO turns (turn not yet visible),
	// causing WaitForTurn to treat this as errTurnNotFound and keep retrying.
	// The 5th read returns the completed turn.
	mock.Handle("thread/read", func(_ json.RawMessage) (any, error) {
		readMu.Lock()
		readCnt++
		cnt := readCnt
		readMu.Unlock()

		if cnt < 5 {
			// Turn not yet in the thread — WaitForTurn retries.
			return map[string]any{
				"thread": map[string]any{
					"id":    "thread-rl",
					"turns": []any{},
				},
			}, nil
		}
		return map[string]any{
			"thread": map[string]any{
				"id": "thread-rl",
				"turns": []any{
					map[string]any{"id": "turn-rl", "status": "completed"},
				},
			},
		}, nil
	})

	// Also push a turn/completed notification to unblock the event-driven path.
	go func() {
		time.Sleep(400 * time.Millisecond)
		_ = mock.Notify("turn/completed", map[string]any{
			"threadId": "thread-rl",
			"turnId":   "turn-rl",
			"status":   "completed",
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	turn, err := client.WaitForTurn(ctx, "thread-rl", "turn-rl")
	if err != nil {
		t.Fatalf("WaitForTurn() returned error after retries: %v", err)
	}
	if turn.ID != "turn-rl" {
		t.Fatalf("unexpected turn ID: %q", turn.ID)
	}
	if turn.Status != codexgo.TurnStatusCompleted {
		t.Fatalf("expected completed status, got %q", turn.Status)
	}
}

// ---- TestThreadFork ----

func TestThreadFork(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-original", "status": "idle"},
		}, nil
	})
	mock.Handle("thread/fork", func(params json.RawMessage) (any, error) {
		var req struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		testutil.MustReadParams(params, &req)
		if req.ThreadID != "thread-original" {
			return nil, fmt.Errorf("unexpected threadId: %q", req.ThreadID)
		}
		return map[string]any{
			"thread": map[string]any{"id": "thread-forked", "status": "idle"},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer st.Close()

	forked, err := st.Fork(ctx, "turn-1")
	if err != nil {
		t.Fatalf("Fork(): %v", err)
	}
	defer forked.Close()

	if forked.ID() == st.ID() {
		t.Fatalf("forked thread should have a different ID; got %q", forked.ID())
	}
	if forked.ID() != "thread-forked" {
		t.Fatalf("unexpected forked thread ID: %q", forked.ID())
	}
}

// ---- TestThreadList ----

func TestThreadList(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/list", func(params json.RawMessage) (any, error) {
		return map[string]any{
			"data": []any{
				map[string]any{"id": "t-list-1", "status": "idle"},
				map[string]any{"id": "t-list-2", "status": "idle"},
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	threads, err := client.ThreadList(ctx, codexgo.ThreadListParams{Limit: 10})
	if err != nil {
		t.Fatalf("ThreadList(): %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	if threads[0].ID != "t-list-1" {
		t.Fatalf("unexpected thread[0] ID: %q", threads[0].ID)
	}
	if threads[1].ID != "t-list-2" {
		t.Fatalf("unexpected thread[1] ID: %q", threads[1].ID)
	}
}

// ---- TestThreadArchive ----

func TestThreadArchive(t *testing.T) {
	client, mock := newClientFromMock(t)

	archived := make(chan string, 1)
	mock.Handle("thread/archive", func(params json.RawMessage) (any, error) {
		var req struct {
			ThreadID string `json:"threadId"`
		}
		testutil.MustReadParams(params, &req)
		select {
		case archived <- req.ThreadID:
		default:
		}
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.ThreadArchive(ctx, codexgo.ThreadArchiveParams{ThreadID: "t-archive"}); err != nil {
		t.Fatalf("ThreadArchive(): %v", err)
	}

	select {
	case tid := <-archived:
		if tid != "t-archive" {
			t.Fatalf("unexpected threadId in archive request: %q", tid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread/archive")
	}
}

// ---- TestSessionThreadSteer ----

func TestSessionThreadSteer(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-steer", "status": "idle"},
		}, nil
	})

	type steerParams struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Input    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"input"`
	}
	steered := make(chan steerParams, 1)
	mock.Handle("turn/steer", func(params json.RawMessage) (any, error) {
		var req steerParams
		testutil.MustReadParams(params, &req)
		select {
		case steered <- req:
		default:
		}
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer st.Close()

	if err := st.Steer(ctx, "turn-steer-1", "additional context"); err != nil {
		t.Fatalf("Steer(): %v", err)
	}

	select {
	case req := <-steered:
		if req.ThreadID != "thread-steer" {
			t.Fatalf("unexpected threadId in steer request: %q", req.ThreadID)
		}
		if req.TurnID != "turn-steer-1" {
			t.Fatalf("unexpected turnId in steer request: %q", req.TurnID)
		}
		if len(req.Input) != 1 || req.Input[0].Type != "text" || req.Input[0].Text != "additional context" {
			t.Fatalf("unexpected input in steer request: %+v", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for turn/steer")
	}
}

// ---- TestSessionThreadRevert ----

func TestSessionThreadRevert(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("thread/start", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"thread": map[string]any{"id": "thread-revert", "status": "idle"},
		}, nil
	})

	type revertParams struct {
		ThreadID     string `json:"threadId"`
		BeforeTurnID string `json:"beforeTurnId"`
	}
	reverted := make(chan revertParams, 1)
	mock.Handle("thread/revert", func(params json.RawMessage) (any, error) {
		var req revertParams
		testutil.MustReadParams(params, &req)
		select {
		case reverted <- req:
		default:
		}
		return map[string]any{
			"thread":               map[string]any{"id": "thread-revert", "status": "idle"},
			"turnsBackwardsCursor": "turns-cur",
			"itemsBackwardsCursor": "items-cur",
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := client.StartThread(ctx)
	if err != nil {
		t.Fatalf("StartThread(): %v", err)
	}
	defer st.Close()

	// The ergonomic wrapper drops the cursors...
	if err := st.Revert(ctx, "turn-rv-2"); err != nil {
		t.Fatalf("Revert(): %v", err)
	}
	select {
	case req := <-reverted:
		if req.ThreadID != "thread-revert" {
			t.Fatalf("unexpected threadId in revert request: %q", req.ThreadID)
		}
		if req.BeforeTurnID != "turn-rv-2" {
			t.Fatalf("unexpected beforeTurnId in revert request: %q", req.BeforeTurnID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for thread/revert")
	}

	// ...while the full form must surface the hydration cursors, since Thread.Turns is
	// always empty after a revert.
	resp, err := client.ThreadRevert(ctx, codexgo.ThreadRevertParams{
		ThreadID:     st.ID(),
		BeforeTurnID: "turn-rv-1",
	})
	if err != nil {
		t.Fatalf("ThreadRevert(): %v", err)
	}
	if resp.Thread.ID != "thread-revert" {
		t.Fatalf("unexpected thread in revert response: %q", resp.Thread.ID)
	}
	if resp.TurnsBackwardsCursor == nil || *resp.TurnsBackwardsCursor != "turns-cur" {
		t.Fatalf("turnsBackwardsCursor not surfaced: %v", resp.TurnsBackwardsCursor)
	}
	if resp.ItemsBackwardsCursor == nil || *resp.ItemsBackwardsCursor != "items-cur" {
		t.Fatalf("itemsBackwardsCursor not surfaced: %v", resp.ItemsBackwardsCursor)
	}
}

// ---- Integration: thread/started notification ----

func TestThreadStartedEvent(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	if err := mock.Notify("thread/started", map[string]any{
		"threadId": "thread-started-1",
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != "thread/started" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.ThreadStartedEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want ThreadStartedEvent", event.Value)
		}
		if typed.ThreadID != "thread-started-1" {
			t.Fatalf("unexpected threadId: %q", typed.ThreadID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for thread/started notification")
	}
}

// ---- Integration: thread/status/changed notification ----

func TestThreadStatusChangedEvent(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	if err := mock.Notify("thread/status/changed", map[string]any{
		"threadId": "thread-sc-1",
		"status":   "idle",
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != "thread/status/changed" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.ThreadStatusChangedEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want ThreadStatusChangedEvent", event.Value)
		}
		if typed.ThreadID != "thread-sc-1" {
			t.Fatalf("unexpected threadId: %q", typed.ThreadID)
		}
		if typed.Status != codexgo.ThreadStatusIdle {
			t.Fatalf("unexpected status: %q", typed.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for thread/status/changed notification")
	}
}

// ---- Integration: a notification upstream has removed ----

// `item/updated` no longer exists upstream (see gen/unknown-methods.txt and plan T1.5).
// The SDK must not pretend to model it: unknown notifications fall back to
// RawNotificationEvent, so callers can still observe them without a stale typed struct.
func TestRemovedNotificationFallsBackToRaw(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	const removed = "item/updated"
	if err := mock.Notify(removed, map[string]any{
		"threadId": "thread-iu-1",
		"turnId":   "turn-iu-1",
		"item":     map[string]any{"id": "item-iu-1", "type": "agentMessage"},
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != removed {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		raw, ok := event.Value.(codexgo.RawNotificationEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want RawNotificationEvent", event.Value)
		}
		if raw.ThreadID != "thread-iu-1" || raw.TurnID != "turn-iu-1" {
			t.Fatalf("unexpected thread/turn IDs: %q / %q", raw.ThreadID, raw.TurnID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s notification", removed)
	}
}

// ---- Deliverable 1: MCP elicitation dispatcher ----

// This replaced the former `item/mcp/requestApproval` handler (plan T1.5 / D3).
//
// With no Elicitation handler installed the reply must be a *decline*, not an error:
// refusing an elicitation is a valid protocol answer that lets the turn continue
// (decision R9).
func TestElicitationDispatcherDeclinesWhenUnhandled(t *testing.T) {
	dispatcher := &codexgo.Dispatcher{}

	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 301, "mcpServer/elicitation/request", map[string]any{
		"threadId":        "t-mcp",
		"turnId":          "turn-mcp",
		"serverName":      "my_server",
		"mode":            "form",
		"message":         "Pick a branch",
		"requestedSchema": map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	var resp codexgo.McpServerElicitationRequestResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, string(result))
	}
	if resp.Action != codexgo.McpServerElicitationActionDecline {
		t.Fatalf("action = %q, want decline", resp.Action)
	}
}

type recordingElicitationHandler struct {
	got chan codexgo.McpServerElicitationRequestParams
}

func (h *recordingElicitationHandler) HandleElicitation(_ context.Context, req codexgo.McpServerElicitationRequestParams) (codexgo.McpServerElicitationRequestResponse, error) {
	h.got <- req
	return codexgo.McpServerElicitationRequestResponse{
		Action:  codexgo.McpServerElicitationActionAccept,
		Content: json.RawMessage(`{"branch":"main"}`),
	}, nil
}

// An installed handler receives the flattened request -- mode discriminator included --
// and its answer is relayed back verbatim.
func TestElicitationDispatcherRoutesToHandler(t *testing.T) {
	handler := &recordingElicitationHandler{got: make(chan codexgo.McpServerElicitationRequestParams, 1)}
	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{Elicitation: handler}))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 302, "mcpServer/elicitation/request", map[string]any{
		"threadId":   "t-el",
		"serverName": "srv",
		"mode":       "openai/userVerification",
		"title":      "Verify device",
		"challenge":  "ch-1",
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	select {
	case req := <-handler.got:
		if req.Mode != codexgo.McpServerElicitationModeUserVerification {
			t.Errorf("mode = %q, want openai/userVerification", req.Mode)
		}
		// The flattened union must survive decoding: title/challenge belong to the
		// userVerification arm only.
		if req.ServerName != "srv" || req.Challenge != "ch-1" || req.Title != "Verify device" {
			t.Errorf("flattened union not preserved: %+v", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never invoked")
	}

	var resp codexgo.McpServerElicitationRequestResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Action != codexgo.McpServerElicitationActionAccept {
		t.Fatalf("action = %q, want accept", resp.Action)
	}
	if string(resp.Content) != `{"branch":"main"}` {
		t.Fatalf("content = %s", resp.Content)
	}
}

// ---- ChatGPT auth token refresh ----

type stubAuthTokensHandler struct {
	got chan codexgo.ChatgptAuthTokensRefreshParams
}

func (h *stubAuthTokensHandler) HandleAuthTokensRefresh(_ context.Context, req codexgo.ChatgptAuthTokensRefreshParams) (codexgo.ChatgptAuthTokensRefreshResponse, error) {
	h.got <- req
	return codexgo.ChatgptAuthTokensRefreshResponse{
		AccessToken:      "fresh-token",
		ChatgptAccountID: "acct-9",
	}, nil
}

func TestAuthTokensRefreshRoutesToHandler(t *testing.T) {
	handler := &stubAuthTokensHandler{got: make(chan codexgo.ChatgptAuthTokensRefreshParams, 1)}
	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{AuthTokens: handler}))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 401, "account/chatgptAuthTokens/refresh", map[string]any{
		"reason":            "unauthorized",
		"previousAccountId": "org-123",
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	select {
	case req := <-handler.got:
		if req.Reason != codexgo.ChatgptAuthTokensRefreshReasonUnauthorized {
			t.Errorf("reason = %q, want unauthorized", req.Reason)
		}
		// The account hint is what lets a multi-account client refresh the right workspace.
		if req.PreviousAccountID != "org-123" {
			t.Errorf("previousAccountId = %q, want org-123", req.PreviousAccountID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler was never invoked")
	}

	var resp codexgo.ChatgptAuthTokensRefreshResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.AccessToken != "fresh-token" || resp.ChatgptAccountID != "acct-9" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// Unlike an approval or an elicitation, a token request has no decline shape. With no
// handler installed the SDK must fail loudly rather than answer with an empty access
// token, which the server would go on to send as a credential.
func TestAuthTokensRefreshWithoutHandlerDoesNotFabricateToken(t *testing.T) {
	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{}))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 402, "account/chatgptAuthTokens/refresh", map[string]any{
		"reason": "unauthorized",
	})
	// The refusal may surface as an RPC error or as an error payload depending on the
	// transport; either is acceptable. What must never happen is a token being invented.
	if err != nil {
		return
	}
	if strings.Contains(string(result), "accessToken") {
		t.Fatalf("no handler is installed, yet the SDK fabricated a token: %s", result)
	}
	// Assert the actual contract now that the harness surfaces error envelopes: a refusal
	// carrying a code, not a bare null that could equally mean "nothing happened".
	var refusal struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(result, &refusal); err != nil {
		t.Fatalf("refusal is not an error envelope: %v (raw: %s)", err, result)
	}
	if refusal.Code == 0 {
		t.Fatalf("refusal has no JSON-RPC error code: %s", result)
	}
	if refusal.Message == "" {
		t.Fatalf("refusal has no message explaining why: %s", result)
	}
}

// TestWithSkillOption was removed together with WithSkill: TurnStartParams has no `skill`
// field upstream, so the option set a field that was never transmitted. The old test only
// asserted the local field, which is precisely why the no-op went unnoticed.

// TestSessionThreadGitDiff was removed along with Client.TurnDiff / SessionThread.GitDiff:
// upstream declares no `turn/diff` request, so the SDK no longer models a pull-style diff
// API. The aggregated diff arrives via the `turn/diff/updated` notification instead, and
// per-file diffs are available from ThreadItem fileChange entries. See plan T1.5.

// ---- Integration: serverRequest/resolved notification ----

func TestServerRequestResolvedEvent(t *testing.T) {
	client, mock := newClientFromMock(t)

	sub := client.Events()
	defer sub.Close()

	time.Sleep(20 * time.Millisecond)

	if err := mock.Notify("serverRequest/resolved", map[string]any{
		"threadId":  "thread-srr-1",
		"requestId": "req-srr-1",
	}); err != nil {
		t.Fatalf("mock.Notify: %v", err)
	}

	select {
	case event := <-sub.C():
		if event.Method != "serverRequest/resolved" {
			t.Fatalf("unexpected event method: %q", event.Method)
		}
		typed, ok := event.Value.(codexgo.ServerRequestResolvedEvent)
		if !ok {
			t.Fatalf("event.Value type = %T, want ServerRequestResolvedEvent", event.Value)
		}
		if typed.ThreadID != "thread-srr-1" {
			t.Fatalf("unexpected threadId: %q", typed.ThreadID)
		}
		if typed.RequestID != "req-srr-1" {
			t.Fatalf("unexpected requestId: %q", typed.RequestID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for serverRequest/resolved notification")
	}
}

// ---- review/start: the target must actually reach the wire ----

// `review/start` previously omitted `target`, which is REQUIRED upstream, so every call was
// rejected. The generated smoke test could not catch it: it passes a zero-value params struct
// and the mock server answers success for any payload. This test asserts the encoded payload.
func TestReviewStartSendsTarget(t *testing.T) {
	title := "Fix the flaky test"
	cases := []struct {
		name   string
		target codexgo.ReviewTarget
		want   map[string]any
	}{
		{
			name:   "uncommittedChanges",
			target: codexgo.UncommittedChangesTarget(),
			want:   map[string]any{"type": "uncommittedChanges"},
		},
		{
			name:   "baseBranch",
			target: codexgo.BaseBranchTarget("main"),
			want:   map[string]any{"type": "baseBranch", "branch": "main"},
		},
		{
			name:   "commit",
			target: codexgo.CommitTarget("abc123", &title),
			want:   map[string]any{"type": "commit", "sha": "abc123", "title": "Fix the flaky test"},
		},
		{
			name:   "custom",
			target: codexgo.CustomTarget("look for off-by-one errors"),
			want:   map[string]any{"type": "custom", "instructions": "look for off-by-one errors"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, mock := newClientFromMock(t)

			got := make(chan map[string]json.RawMessage, 1)
			mock.Handle("review/start", func(params json.RawMessage) (any, error) {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(params, &raw); err != nil {
					return nil, err
				}
				got <- raw
				return map[string]any{
					"reviewThreadId": "review-thr-1",
					"turn":           map[string]any{"id": "turn-r1", "status": "inProgress"},
				}, nil
			})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			resp, err := client.ReviewStart(ctx, codexgo.ReviewStartParams{
				ThreadID: "thr-1",
				Target:   tc.target,
			})
			if err != nil {
				t.Fatalf("ReviewStart: %v", err)
			}

			select {
			case raw := <-got:
				// `target` is required; its absence is the bug this pins.
				targetRaw, present := raw["target"]
				if !present {
					t.Fatalf("target was not sent; payload = %v", raw)
				}
				var sent map[string]any
				if err := json.Unmarshal(targetRaw, &sent); err != nil {
					t.Fatalf("target is not an object: %s", targetRaw)
				}
				for k, want := range tc.want {
					if sent[k] != want {
						t.Errorf("target.%s = %v, want %v", k, sent[k], want)
					}
				}
				// Invented fields must not reappear.
				if _, ok := raw["turnId"]; ok {
					t.Error("turnId is not an upstream field and must not be sent")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("review/start was never called")
			}

			// The response used to be discarded; a detached review is only addressable
			// through ReviewThreadID.
			if resp.ReviewThreadID != "review-thr-1" {
				t.Errorf("ReviewThreadID = %q, want review-thr-1", resp.ReviewThreadID)
			}
			if resp.Turn.ID != "turn-r1" {
				t.Errorf("Turn.ID = %q, want turn-r1", resp.Turn.ID)
			}
		})
	}
}

// ---- thread/resume: excludeTurns is a boolean, and the rest of the field set is real ----

// ThreadResumeParams previously carried four fields that do not exist upstream and typed
// excludeTurns as []string where upstream is a plain boolean, so it could be neither set
// correctly nor sent (the empty slice was omitempty). This pins the encoded payload.
func TestThreadResumeSendsUpstreamFieldSet(t *testing.T) {
	client, mock := newClientFromMock(t)

	got := make(chan map[string]json.RawMessage, 1)
	mock.Handle("thread/resume", func(params json.RawMessage) (any, error) {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(params, &raw); err != nil {
			return nil, err
		}
		got <- raw
		return map[string]any{"thread": map[string]any{"id": "thr-r"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sandbox := codexgo.SandboxWorkspaceWrite
	if _, err := client.ThreadResume(ctx, codexgo.ThreadResumeParams{
		ThreadID:         "thr-r",
		ExcludeTurns:     true,
		Model:            "gpt-5.1",
		ModelProvider:    "openai",
		Sandbox:          &sandbox,
		ServiceTier:      "priority",
		BaseInstructions: "be terse",
		Config:           map[string]any{"model": "gpt-5.1"},
	}); err != nil {
		t.Fatalf("ThreadResume: %v", err)
	}

	select {
	case raw := <-got:
		// The bug: a boolean, not an array.
		if v, ok := raw["excludeTurns"]; !ok || string(v) != "true" {
			t.Errorf("excludeTurns = %s (present=%v), want true", v, ok)
		}
		for field, want := range map[string]string{
			"threadId":         `"thr-r"`,
			"model":            `"gpt-5.1"`,
			"modelProvider":    `"openai"`,
			"sandbox":          `"workspace-write"`,
			"serviceTier":      `"priority"`,
			"baseInstructions": `"be terse"`,
		} {
			if string(raw[field]) != want {
				t.Errorf("%s = %s, want %s", field, raw[field], want)
			}
		}
		// The invented fields must not reappear.
		for _, gone := range []string{"history", "path", "initialTurnsPage"} {
			if _, ok := raw[gone]; ok {
				t.Errorf("%q is not an upstream field and must not be sent", gone)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("thread/resume was never called")
	}
}

// ---- turn/start sandboxPolicy: an object, not a mode string ----

// `sandboxPolicy` is a tagged OBJECT upstream, with camelCase discriminators. The SDK used to
// send the bare SandboxMode string ("workspace-write"), which is rejected twice over: wrong
// JSON type and wrong value. This asserts the encoded payload.
func TestTurnStartSendsSandboxPolicyObject(t *testing.T) {
	client, mock := newClientFromMock(t)

	got := make(chan map[string]json.RawMessage, 1)
	mock.Handle("turn/start", func(params json.RawMessage) (any, error) {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(params, &raw); err != nil {
			return nil, err
		}
		got <- raw
		return map[string]any{"turn": map[string]any{"id": "turn-s"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := client.TurnStart(ctx, codexgo.TurnStartParams{
		ThreadID: "thr-s",
		Input:    "hi",
		SandboxPolicy: func() *codexgo.SandboxPolicy {
			p := codexgo.WorkspaceWritePolicy()
			p.WritableRoots = []string{"/tmp/w"}
			return &p
		}(),
	}); err != nil {
		t.Fatalf("TurnStart: %v", err)
	}

	select {
	case raw := <-got:
		sent, ok := raw["sandboxPolicy"]
		if !ok {
			t.Fatal("sandboxPolicy was not sent")
		}
		if len(sent) == 0 || sent[0] != '{' {
			t.Fatalf("sandboxPolicy must be an object, got %s", sent)
		}
		var policy struct {
			Type          string   `json:"type"`
			WritableRoots []string `json:"writableRoots"`
		}
		if err := json.Unmarshal(sent, &policy); err != nil {
			t.Fatalf("unmarshal sandboxPolicy: %v", err)
		}
		// The discriminator, not the SandboxMode spelling.
		if policy.Type != codexgo.SandboxPolicyTypeWorkspaceWrite {
			t.Errorf("type = %q, want %q", policy.Type, codexgo.SandboxPolicyTypeWorkspaceWrite)
		}
		if len(policy.WritableRoots) != 1 || policy.WritableRoots[0] != "/tmp/w" {
			t.Errorf("writableRoots = %v", policy.WritableRoots)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("turn/start was never called")
	}
}
