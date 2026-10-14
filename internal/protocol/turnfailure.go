package protocol

import "encoding/json"

// CodexErrorKind is the classification the Codex app-server puts on a turn failure in
// `error.codexErrorInfo` (also mirrored at `turn/completed` -> `turn.error.codexErrorInfo`).
//
// Upstream models that field as a union: the unit kinds below arrive as plain strings, and
// four kinds carry an HTTP status inside a single-key object:
//
//	{"httpConnectionFailed": {"httpStatusCode": 500}}
//
// ParseCodexErrorInfo normalises both shapes. The value is what a caller keys off to decide
// whether a failed turn is worth retrying against different infrastructure - e.g.
// CodexErrorInternalServer or CodexErrorUnauthorized - as opposed to a deterministic failure
// such as a rejected prompt.
//
// Note the upstream mapping is lossy: a provider's HTTP 400 (including "unknown model") and an
// invalid prompt both collapse to CodexErrorOther, and a torn response stream may too. A caller
// that must not retry deterministic failures should therefore treat CodexErrorOther and
// CodexErrorUnknown as non-retryable.
type CodexErrorKind string

const (
	// CodexErrorUnknown is the absence of a classification: no codexErrorInfo was sent, or it
	// could not be decoded.
	CodexErrorUnknown CodexErrorKind = ""
	// CodexErrorOther is upstream's catch-all, which swallows several distinct causes.
	CodexErrorOther CodexErrorKind = "other"
	// CodexErrorInternalServer is an upstream HTTP 500 from the model provider.
	CodexErrorInternalServer CodexErrorKind = "internalServerError"
	// CodexErrorServerOverloaded is an upstream "server_is_overloaded" 503.
	CodexErrorServerOverloaded CodexErrorKind = "serverOverloaded"
	// CodexErrorTooManyFailedAttempts is an exhausted retry budget; the object carries the
	// HTTP status (429 for a rate limit).
	CodexErrorTooManyFailedAttempts CodexErrorKind = "responseTooManyFailedAttempts"
	// CodexErrorHTTPConnectionFailed is a failed HTTP connection; the object carries the HTTP
	// status when the server saw one.
	CodexErrorHTTPConnectionFailed CodexErrorKind = "httpConnectionFailed"
	// CodexErrorResponseStreamConnectionFailed is a failed response stream connection; the
	// object carries the HTTP status.
	CodexErrorResponseStreamConnectionFailed CodexErrorKind = "responseStreamConnectionFailed"
	// CodexErrorResponseStreamDisconnected is a mid-turn stream disconnect.
	CodexErrorResponseStreamDisconnected CodexErrorKind = "responseStreamDisconnected"
	// CodexErrorUnauthorized is an authentication/authorization failure (the provider rejected
	// the credentials).
	CodexErrorUnauthorized CodexErrorKind = "unauthorized"
	// CodexErrorBadRequest is a rejected request.
	CodexErrorBadRequest CodexErrorKind = "badRequest"
	// CodexErrorContextWindowExceeded is a prompt that overflowed the model's context window.
	CodexErrorContextWindowExceeded CodexErrorKind = "contextWindowExceeded"
	// CodexErrorUsageLimitExceeded is a quota/usage-limit rejection.
	CodexErrorUsageLimitExceeded CodexErrorKind = "usageLimitExceeded"
	// CodexErrorCyberPolicy is a policy refusal.
	CodexErrorCyberPolicy CodexErrorKind = "cyberPolicy"
)

// ParseCodexErrorInfo decodes a raw `codexErrorInfo` value into its kind and, when the kind
// carries one, its HTTP status. An absent, empty, `null` or unrecognised value yields
// CodexErrorUnknown with a zero status.
func ParseCodexErrorInfo(raw json.RawMessage) (CodexErrorKind, int) {
	if len(raw) == 0 || string(raw) == "null" {
		return CodexErrorUnknown, 0
	}
	// Unit variant: a bare string such as "internalServerError".
	var unit string
	if err := json.Unmarshal(raw, &unit); err == nil {
		return CodexErrorKind(unit), 0
	}
	// Data variant: exactly one key naming the kind, whose value carries the HTTP status.
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || len(object) != 1 {
		return CodexErrorUnknown, 0
	}
	for kind, payload := range object {
		var details struct {
			HTTPStatusCode int `json:"httpStatusCode"`
		}
		_ = json.Unmarshal(payload, &details)
		return CodexErrorKind(kind), details.HTTPStatusCode
	}
	return CodexErrorUnknown, 0
}

// Classify reports this error notification's provider classification and HTTP status, if any.
// It reads the nested v2 payload (`error.codexErrorInfo`) only; the legacy flat `Code` field is
// not a codexErrorInfo value.
func (e ErrorEvent) Classify() (CodexErrorKind, int) {
	if e.Error == nil || e.Error.CodexErrorInfo == nil {
		return CodexErrorUnknown, 0
	}
	return ParseCodexErrorInfo(*e.Error.CodexErrorInfo)
}

// ClassifyTurnError classifies a failed turn's `error` payload (the hand-written Turn carries
// it as raw JSON) and returns the kind, any HTTP status, and the human-readable message.
func ClassifyTurnError(t *Turn) (CodexErrorKind, int, string) {
	if t == nil || len(t.Error) == 0 || string(t.Error) == "null" {
		return CodexErrorUnknown, 0, ""
	}
	var payload struct {
		Message        string          `json:"message"`
		CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
	}
	if err := json.Unmarshal(t.Error, &payload); err != nil {
		// Not the structured shape: keep the raw text as the message rather than losing it.
		return CodexErrorUnknown, 0, string(t.Error)
	}
	kind, status := ParseCodexErrorInfo(payload.CodexErrorInfo)
	return kind, status, payload.Message
}
