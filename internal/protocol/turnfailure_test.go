package protocol

import (
	"encoding/json"
	"testing"
)

// TestParseCodexErrorInfo covers both arms of upstream's union: unit kinds arrive as a bare
// string, and four kinds carry an HTTP status inside a single-key object.
func TestParseCodexErrorInfo(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantKind   CodexErrorKind
		wantStatus int
	}{
		{"unit internal server error", `"internalServerError"`, CodexErrorInternalServer, 0},
		{"unit other", `"other"`, CodexErrorOther, 0},
		{"unit unauthorized", `"unauthorized"`, CodexErrorUnauthorized, 0},
		{"data http connection failed", `{"httpConnectionFailed":{"httpStatusCode":500}}`, CodexErrorHTTPConnectionFailed, 500},
		{"data too many failed attempts", `{"responseTooManyFailedAttempts":{"httpStatusCode":429}}`, CodexErrorTooManyFailedAttempts, 429},
		{"data without a status", `{"responseStreamConnectionFailed":{}}`, CodexErrorResponseStreamConnectionFailed, 0},
		{"null", `null`, CodexErrorUnknown, 0},
		{"empty object", `{}`, CodexErrorUnknown, 0},
		{"two keys is not a single-key union", `{"a":{},"b":{}}`, CodexErrorUnknown, 0},
		{"garbage", `[1,2,3]`, CodexErrorUnknown, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, status := ParseCodexErrorInfo(json.RawMessage(tc.raw))
			if kind != tc.wantKind || status != tc.wantStatus {
				t.Fatalf("ParseCodexErrorInfo(%s) = (%q, %d), want (%q, %d)",
					tc.raw, kind, status, tc.wantKind, tc.wantStatus)
			}
		})
	}

	// An absent value is unknown, not a panic.
	if kind, status := ParseCodexErrorInfo(nil); kind != CodexErrorUnknown || status != 0 {
		t.Fatalf("ParseCodexErrorInfo(nil) = (%q, %d), want unknown", kind, status)
	}
}

// TestErrorEventClassify pins that an error notification's nested codexErrorInfo is read, and
// that an event without one classifies as unknown rather than guessing from the flat Code.
func TestErrorEventClassify(t *testing.T) {
	info := json.RawMessage(`{"httpConnectionFailed":{"httpStatusCode":503}}`)
	event := ErrorEvent{Error: &TurnError{Message: "boom", CodexErrorInfo: &info}}
	kind, status := event.Classify()
	if kind != CodexErrorHTTPConnectionFailed || status != 503 {
		t.Fatalf("Classify() = (%q, %d), want (%q, 503)", kind, status, CodexErrorHTTPConnectionFailed)
	}

	if kind, status := (ErrorEvent{Code: "legacy_flat_code"}).Classify(); kind != CodexErrorUnknown || status != 0 {
		t.Fatalf("Classify() without codexErrorInfo = (%q, %d), want unknown", kind, status)
	}
	if kind, _ := (ErrorEvent{}).Classify(); kind != CodexErrorUnknown {
		t.Fatalf("Classify() on an empty event = %q, want unknown", kind)
	}
}

// TestClassifyTurnError covers the turn/completed shape, where the hand-written Turn carries
// the error as raw JSON.
func TestClassifyTurnError(t *testing.T) {
	turn := &Turn{Status: TurnStatusFailed, Error: json.RawMessage(
		`{"message":"unknown model 'gpt-6.1-sol'","codexErrorInfo":"other"}`)}
	kind, status, message := ClassifyTurnError(turn)
	if kind != CodexErrorOther || status != 0 {
		t.Fatalf("ClassifyTurnError kind/status = (%q, %d), want (%q, 0)", kind, status, CodexErrorOther)
	}
	if message != "unknown model 'gpt-6.1-sol'" {
		t.Fatalf("ClassifyTurnError message = %q", message)
	}

	if kind, status, message := ClassifyTurnError(&Turn{}); kind != CodexErrorUnknown || status != 0 || message != "" {
		t.Fatalf("ClassifyTurnError on a turn without error = (%q, %d, %q)", kind, status, message)
	}
	if kind, _, message := ClassifyTurnError(nil); kind != CodexErrorUnknown || message != "" {
		t.Fatalf("ClassifyTurnError(nil) = (%q, %q)", kind, message)
	}

	// An unstructured payload must not lose its text.
	if _, _, message := ClassifyTurnError(&Turn{Error: json.RawMessage(`"plain failure"`)}); message != `"plain failure"` {
		t.Fatalf("unstructured turn error message = %q", message)
	}
}
