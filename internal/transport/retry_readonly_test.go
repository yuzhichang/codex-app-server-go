package transport

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// ErrClosed used to be unconditionally non-retryable, so every call in flight when a
// connection dropped failed hard, even a read that could simply be re-issued. It is now
// retried for read-only methods (plan T2.3).
func TestErrClosedRetriesReadOnlyMethods(t *testing.T) {
	readOnly := []string{
		"thread/read",
		"thread/turns/list",
		"config/read",
		"fs/readFile",
		"fuzzyFileSearch",
		"mcpServerStatus/list",
	}
	for _, method := range readOnly {
		inner := &mockInner{errs: []error{ErrClosed, ErrClosed}}
		rt := NewRetryTransport(inner, RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})

		if err := rt.Call(context.Background(), method, nil, nil); err != nil {
			t.Fatalf("%s: Call = %v, want success after retries", method, err)
		}
		if got := atomic.LoadInt32(&inner.calls); got != 3 {
			t.Fatalf("%s: attempts = %d, want 3", method, got)
		}
	}
}

// The other half of the rule, and the more important one: a dropped connection must never
// cause a mutation to be replayed. The request may already have been applied server-side.
func TestErrClosedDoesNotRetryMutations(t *testing.T) {
	mutations := []string{
		"thread/start",
		"turn/start",
		"thread/revert",
		"config/value/write",
		"config/batchWrite",
		"plugin/install",
		"plugin/uninstall",
		"marketplace/add",
		"fs/writeFile",
		"fs/remove",
		"account/logout",
		"command/exec",
	}
	for _, method := range mutations {
		inner := &mockInner{errs: []error{ErrClosed, ErrClosed, ErrClosed}}
		rt := NewRetryTransport(inner, RetryConfig{MaxAttempts: 5, BaseDelay: time.Millisecond})

		err := rt.Call(context.Background(), method, nil, nil)
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("%s: err = %v, want ErrClosed surfaced", method, err)
		}
		if got := atomic.LoadInt32(&inner.calls); got != 1 {
			t.Fatalf("%s: attempts = %d, want 1 (must not replay a mutation)", method, got)
		}
	}
}

// Errors the server reports are *responses*: the request was answered, so re-issuing cannot
// double-apply and the method's mutating nature is irrelevant.
func TestServerReportedErrorsRetryAnyMethod(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "rate limited",
			err: &RPCError{
				Code:    -32000,
				Message: "too many requests",
				Data:    json.RawMessage(`{"codexErrorInfo":{"httpStatusCode":429}}`),
			},
		},
		{
			name: "http connection failed",
			err: &RPCError{
				Code:    -32000,
				Message: "connection failed",
				Data:    json.RawMessage(`{"codexErrorInfo":{"type":"HttpConnectionFailed"}}`),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A mutating method, to show the method does not gate server-reported errors.
			inner := &mockInner{errs: []error{tc.err}}
			rt := NewRetryTransport(inner, RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond})

			if err := rt.Call(context.Background(), "turn/start", nil, nil); err != nil {
				t.Fatalf("Call = %v, want success on the second attempt", err)
			}
			if got := atomic.LoadInt32(&inner.calls); got != 2 {
				t.Fatalf("attempts = %d, want 2", got)
			}
		})
	}
}

// Guards the shape of the allow-list itself: it is an explicit list precisely so it cannot
// silently grow to cover everything.
func TestReadOnlyMethodSetIsTargeted(t *testing.T) {
	if len(readOnlyMethods) > 60 {
		t.Fatalf("readOnlyMethods has %d entries; it looks like it is being widened carelessly", len(readOnlyMethods))
	}
	// IsReadOnlyMethod is the exported entry point; keep it consistent with the map.
	for m := range readOnlyMethods {
		if !IsReadOnlyMethod(m) {
			t.Fatalf("IsReadOnlyMethod(%q) = false but it is in the set", m)
		}
	}
	if IsReadOnlyMethod("turn/start") {
		t.Fatal("IsReadOnlyMethod reports a mutation as read-only")
	}
}
