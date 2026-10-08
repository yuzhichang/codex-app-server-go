package transport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// D1 / decision R3.
//
// Upstream app-server is explicit that it does not use true JSON-RPC 2.0:
//
//	//! We do not do true JSON-RPC 2.0, as we neither send nor expect the
//	//! "jsonrpc": "2.0" field.        -- app-server-protocol/src/rpc.rs:1-2
//
// Every envelope we control must therefore serialize without a "jsonrpc" field.
// (The stdio transport is the one exception: it is backed by jrpc2, which is a strict
// JSON-RPC 2.0 implementation. See the note in transport.go.)
func TestOutboundEnvelopesHaveNoJSONRPCField(t *testing.T) {
	cases := []struct {
		name    string
		payload any
	}{
		{"request", requestEnvelope{Method: "thread/start", ID: 1, Params: map[string]any{"a": 1}}},
		{"notification", notificationEnvelope{Method: "initialized"}},
		{"reply", replyEnvelope{ID: json.RawMessage("1"), Result: map[string]any{"ok": true}}},
		{"reply-error", replyEnvelope{ID: json.RawMessage("1"), Error: &RPCError{Code: -32601, Message: "nope"}}},
		{"call-with-id", callEnvelope{Method: "turn/start"}.withID(3)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(data), "jsonrpc") {
				t.Errorf("outbound payload must not contain a jsonrpc field, got: %s", data)
			}
		})
	}
}

// The W3C trace context rides on requests only: upstream JSONRPCRequest has an optional
// `trace`, while JSONRPCNotification has none.
func TestTraceContextRidesOnRequestsOnly(t *testing.T) {
	tc := &TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", Tracestate: "vendor=1"}
	ctx := WithTraceContext(context.Background(), tc)

	got := TraceContextFromContext(ctx)
	if got == nil {
		t.Fatal("trace context lost")
	}
	if got.Traceparent != tc.Traceparent || got.Tracestate != tc.Tracestate {
		t.Fatalf("trace context mangled: %+v", got)
	}

	env := requestEnvelope{Method: "turn/start", ID: 1, Trace: TraceContextFromContext(ctx)}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"trace":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"`) {
		t.Fatalf("trace not serialized on request: %s", data)
	}

	// Notifications must not gain a trace field.
	noteData, err := json.Marshal(notificationEnvelope{Method: "initialized"})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	if strings.Contains(string(noteData), "trace") {
		t.Fatalf("notification must not carry trace: %s", noteData)
	}
}

func TestTraceContextHelpersAreNilSafe(t *testing.T) {
	if TraceContextFromContext(nil) != nil {
		t.Error("nil ctx must yield nil trace")
	}
	if TraceContextFromContext(context.Background()) != nil {
		t.Error("ctx without trace must yield nil trace")
	}
	base := context.Background()
	if returned := WithTraceContext(base, nil); returned != base {
		t.Error("WithTraceContext(ctx, nil) must return the original context unchanged")
	}
	// An unrelated value must not be mistaken for a trace context.
	if got := TraceContextFromContext(context.WithValue(base, traceContextKey{}, "not-a-trace")); got != nil {
		t.Errorf("expected nil for a non-trace value, got %+v", got)
	}
}
