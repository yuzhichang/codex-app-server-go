package codexgo_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// Plan §5.8 / T2.13. A server-initiated request the SDK cannot route to a configured handler
// must be answered in a way that lets the session continue, and must be reported so the
// application learns the server asked something.

// The legacy `applyPatchApproval` is declared stable upstream but deliberately not
// implemented as a feature (decision R4). It still has to be answered with a *valid*
// decision: a JSON-RPC error can end the turn.
func TestUnhandledLegacyApprovalIsDeniedNotErrored(t *testing.T) {
	// Both legacy approvals are declared stable but are not implemented as features (R4).
	// They must still be *answered*, and with a valid decision: a JSON-RPC error can end the
	// turn, whereas `denied` lets the session continue and try something else.
	for _, method := range []string{"applyPatchApproval", "execCommandApproval"} {
		t.Run(method, func(t *testing.T) {
			_, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{}))
			time.Sleep(20 * time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			result, err := mock.RequestAndWait(ctx, 201, method, map[string]any{
				"threadId": "t-1",
				"turnId":   "turn-1",
			})
			if err != nil {
				t.Fatalf("a JSON-RPC error was returned, which can end the turn: %v", err)
			}

			// The wire shape is the whole point of this test. Upstream's
			// ReviewDecision::Denied is a STRUCT variant, so it serializes as
			// {"denied":{"rejection":"..."}} -- not as the bare string "denied", which no
			// unit variant matches.
			var got struct {
				Decision struct {
					Denied *struct {
						Rejection string `json:"rejection"`
					} `json:"denied"`
				} `json:"decision"`
			}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("unmarshal: %v (raw: %s)", err, result)
			}
			if got.Decision.Denied == nil {
				t.Fatalf("reply has no `denied` object: %s", result)
			}
			if got.Decision.Denied.Rejection != "denied" {
				t.Fatalf("rejection = %q, want %q", got.Decision.Denied.Rejection, "denied")
			}
			// Guard against the tempting wrong answers: a bare string is what the Rust enum's
			// snake_case casing suggests and would be rejected, and `abort` stops the session.
			if strings.Contains(string(result), `"decision":"denied"`) {
				t.Fatalf("sent the bare-string form, which no ReviewDecision variant accepts: %s", result)
			}
			if strings.Contains(string(result), "abort") {
				t.Fatalf("reply must not abort the session: %s", result)
			}
		})
	}
}

// The same request must also be reported, or an application would never learn that the
// server asked it to approve something.
func TestUnhandledRequestEmitsEvent(t *testing.T) {
	client, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{}))
	sub := client.Events()
	defer sub.Close()
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := mock.RequestAndWait(ctx, 202, "applyPatchApproval", map[string]any{
		"threadId": "t-ev", "turnId": "turn-ev",
	}); err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatal("event stream closed before the unhandled-request event arrived")
			}
			if ev.Method != codexgo.EventMethodUnhandledServerRequest {
				continue
			}
			report, ok := ev.Value.(codexgo.UnhandledServerRequestEvent)
			if !ok {
				t.Fatalf("event value = %T", ev.Value)
			}
			if report.Method != "applyPatchApproval" {
				t.Errorf("Method = %q", report.Method)
			}
			if report.Action != "declined" {
				t.Errorf("Action = %q, want declined", report.Action)
			}
			// The ids come from the payload, so an operator can tell which turn was affected.
			if report.ThreadID != "t-ev" || report.TurnID != "turn-ev" {
				t.Errorf("thread/turn not extracted: %+v", report)
			}
			if report.Reason == "" {
				t.Error("Reason is empty; it is what tells a caller why nothing ran")
			}
			return
		case <-deadline:
			t.Fatal("no unhandled-request event was emitted")
		}
	}
}

// An unknown method cannot be answered with a valid payload, so it must be reported as
// "method not found" rather than a generic failure the server may read as a broken session.
func TestUnknownServerRequestIsMethodNotFound(t *testing.T) {
	_, mock := newClientFromMock(t, codexgo.WithRequestHandler(&codexgo.Dispatcher{}))

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// The mock hands back the raw reply whether it is a result or an error, so the assertion
	// is on the payload.
	result, err := mock.RequestAndWait(ctx, 203, "totally/madeUp", map[string]any{})
	if err != nil {
		t.Fatalf("RequestAndWait: %v", err)
	}
	if !strings.Contains(string(result), "-32601") {
		t.Fatalf("reply should be -32601 (method not found), got: %s", result)
	}
	// A generic -32603 would be read as a broken session rather than an unknown method.
	if strings.Contains(string(result), "-32603") {
		t.Fatalf("used -32603 where -32601 is meant: %s", result)
	}
}
