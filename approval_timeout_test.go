package codexgo_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// blockingExecHandler never answers until its context is done.
type blockingExecHandler struct{ started chan struct{} }

func (h *blockingExecHandler) HandleCommandExecutionApproval(ctx context.Context, _ codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return codexgo.CommandExecutionApprovalResult{}, ctx.Err()
}

// Permissive: it would grant everything if it were ever allowed to answer.
type permissiveExecHandler struct{}

func (permissiveExecHandler) HandleCommandExecutionApproval(context.Context, codexgo.CommandExecutionApprovalRequest) (codexgo.CommandExecutionApprovalResult, error) {
	return codexgo.CommandExecutionApprovalResult{Decision: codexgo.CommandExecutionApprovalDecisionAccept}, nil
}

// A handler that never returns must not hang the turn forever. The SDK answers on its behalf,
// and the answer is the same refusal it would give with no handler at all -- a timeout must
// never grant anything.
func TestApprovalTimeoutRefusesRatherThanHanging(t *testing.T) {
	handler := &blockingExecHandler{started: make(chan struct{}, 1)}
	dispatcher := &codexgo.Dispatcher{
		Exec:            handler,
		ApprovalTimeout: 150 * time.Millisecond,
	}

	client, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))
	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result json.RawMessage
	go func() {
		defer close(done)
		result, _ = mock.RequestAndWait(ctx, 401, "item/commandExecution/requestApproval", map[string]any{
			"threadId": "t-to", "turnId": "turn-to", "itemId": "i-to",
		})
	}()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("the request was never answered: a stuck handler hung the turn")
	}

	// The handler was actually entered, so this is testing the timeout rather than a
	// handler that was never called.
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("handler was never invoked")
	}

	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, result)
	}
	if resp.Decision != string(codexgo.CommandExecutionApprovalDecisionDecline) {
		t.Fatalf("decision = %q, want a decline: a timeout must never grant", resp.Decision)
	}
}

// A handler that answers in time is unaffected, and its answer is relayed.
func TestApprovalTimeoutLeavesPromptHandlersAlone(t *testing.T) {
	dispatcher := &codexgo.Dispatcher{
		Exec:            permissiveExecHandler{},
		ApprovalTimeout: 5 * time.Second,
	}

	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(dispatcher))
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := mock.RequestAndWait(ctx, 402, "item/commandExecution/requestApproval", map[string]any{
		"threadId": "t-ok", "turnId": "turn-ok", "itemId": "i-ok",
	})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}
	if !strings.Contains(string(result), string(codexgo.CommandExecutionApprovalDecisionAccept)) {
		t.Fatalf("a prompt handler's answer was not relayed: %s", result)
	}
}
