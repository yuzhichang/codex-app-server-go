package protocol

import (
	"encoding/json"

	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
)

// Enum / data type aliases -- canonical definitions live in the generated schema package.

type ThreadStatus = schematypes.ThreadStatus
type ThreadActiveFlag = schematypes.ThreadActiveFlag
type TurnStatus = schematypes.TurnStatus
type TurnItemsView = schematypes.TurnItemsView
type ItemKind = schematypes.ItemKind
type TurnError = schematypes.TurnError

// ThreadTokenUsage is the per-thread usage snapshot upstream sends on
// `thread/tokenUsage/updated`. NOTE: there is deliberately no flat `TokenUsage` type --
// upstream carries usage nowhere else (turn/completed sends only {threadId, turn}, and the
// Turn struct has no usage field), so a flat total would model something that never arrives.
type ThreadTokenUsage = schematypes.ThreadTokenUsage
type TokenUsageBreakdown = schematypes.TokenUsageBreakdown

// Re-export constants so existing call-sites inside the protocol package compile.

const (
	ThreadStatusNotLoaded   = schematypes.ThreadStatusNotLoaded
	ThreadStatusIdle        = schematypes.ThreadStatusIdle
	ThreadStatusSystemError = schematypes.ThreadStatusSystemError
	ThreadStatusActive      = schematypes.ThreadStatusActive
)

const (
	ThreadActiveFlagWaitingOnApproval  = schematypes.ThreadActiveFlagWaitingOnApproval
	ThreadActiveFlagWaitingOnUserInput = schematypes.ThreadActiveFlagWaitingOnUserInput
)

const (
	TurnStatusCompleted   = schematypes.TurnStatusCompleted
	TurnStatusInterrupted = schematypes.TurnStatusInterrupted
	TurnStatusFailed      = schematypes.TurnStatusFailed
	TurnStatusInProgress  = schematypes.TurnStatusInProgress
)

const (
	TurnItemsViewNotLoaded = schematypes.TurnItemsViewNotLoaded
	TurnItemsViewSummary   = schematypes.TurnItemsViewSummary
	TurnItemsViewFull      = schematypes.TurnItemsViewFull
)

const (
	ItemKindUserMessage         = schematypes.ItemKindUserMessage
	ItemKindHookPrompt          = schematypes.ItemKindHookPrompt
	ItemKindAgentMessage        = schematypes.ItemKindAgentMessage
	ItemKindPlan                = schematypes.ItemKindPlan
	ItemKindReasoning           = schematypes.ItemKindReasoning
	ItemKindCommandExecution    = schematypes.ItemKindCommandExecution
	ItemKindFileChange          = schematypes.ItemKindFileChange
	ItemKindMCPToolCall         = schematypes.ItemKindMCPToolCall
	ItemKindDynamicToolCall     = schematypes.ItemKindDynamicToolCall
	ItemKindCollabAgentToolCall = schematypes.ItemKindCollabAgentToolCall
	ItemKindSubAgentActivity    = schematypes.ItemKindSubAgentActivity
	ItemKindWebSearch           = schematypes.ItemKindWebSearch
	ItemKindImageView           = schematypes.ItemKindImageView
	ItemKindSleep               = schematypes.ItemKindSleep
	ItemKindImageGeneration     = schematypes.ItemKindImageGeneration
	ItemKindEnteredReviewMode   = schematypes.ItemKindEnteredReviewMode
	ItemKindExitedReviewMode    = schematypes.ItemKindExitedReviewMode
	ItemKindContextCompaction   = schematypes.ItemKindContextCompaction
)

// Event types (server-push notification payloads; not in the protocol schema).

type TurnStartedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
	TurnID   string `json:"turnId,omitempty"`
	Turn     *Turn  `json:"turn,omitempty"`
}

// TurnCompletedEvent mirrors upstream `turn/completed`, whose payload is exactly
// {threadId, turn}. Status and TurnID are convenience fields derived from the nested turn
// by the SDK, not separate wire fields.
type TurnCompletedEvent struct {
	ThreadID string     `json:"threadId,omitempty"`
	TurnID   string     `json:"turnId,omitempty"`
	Status   TurnStatus `json:"status,omitempty"`
	Turn     *Turn      `json:"turn,omitempty"`
}

type ItemStartedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
	TurnID   string `json:"turnId,omitempty"`
	Item     *Item  `json:"item,omitempty"`
}

type ItemCompletedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
	TurnID   string `json:"turnId,omitempty"`
	Item     *Item  `json:"item,omitempty"`
}

// ThreadTokenUsageUpdatedEvent mirrors upstream `ThreadTokenUsageUpdatedNotification`:
// {threadId, turnId, tokenUsage}. This is the only channel that carries token usage.
type ThreadTokenUsageUpdatedEvent struct {
	ThreadID   string            `json:"threadId,omitempty"`
	TurnID     string            `json:"turnId,omitempty"`
	TokenUsage *ThreadTokenUsage `json:"tokenUsage,omitempty"`
}

type TurnDiffUpdatedEvent struct {
	ThreadID string          `json:"threadId,omitempty"`
	TurnID   string          `json:"turnId,omitempty"`
	Diff     json.RawMessage `json:"diff,omitempty"`
}

type TurnPlanUpdatedEvent struct {
	ThreadID string          `json:"threadId,omitempty"`
	TurnID   string          `json:"turnId,omitempty"`
	Plan     json.RawMessage `json:"plan,omitempty"`
}

// ErrorEvent mirrors upstream `ErrorNotification`
// (app-server-protocol/src/protocol/v2/notification.rs), which the v2 schema aggregate
// does not export. Two details matter and were previously lost:
//
//   - the message is NESTED under `error` (TurnError), not a top-level `message`; and
//   - `willRetry` says the server retries the turn IN PLACE — upstream documents it as
//     "if true, this will not interrupt a turn".
type ErrorEvent struct {
	ThreadID string `json:"threadId,omitempty"`
	TurnID   string `json:"turnId,omitempty"`
	// Error carries the detail: message, codexErrorInfo, additionalDetails.
	Error *TurnError `json:"error,omitempty"`
	// WillRetry marks a transient error the server recovers from on its own; such an
	// error does not end the turn.
	WillRetry bool `json:"willRetry,omitempty"`
	// Message / Code / Data are the older flat shape. A v2 server nests the detail under
	// Error instead, so a reader should use Detail() rather than Message.
	Message string          `json:"message,omitempty"`
	Code    string          `json:"code,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Detail returns the most specific message available for this error notification.
func (e ErrorEvent) Detail() string {
	if e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if e.Message != "" {
		return e.Message
	}
	if len(e.Data) > 0 {
		return string(e.Data)
	}
	return ""
}

// ErrorCode returns the upstream classification when the server sent one
// (codexErrorInfo), falling back to the legacy flat code.
func (e ErrorEvent) ErrorCode() string {
	if e.Error != nil && e.Error.CodexErrorInfo != nil {
		if raw, err := json.Marshal(e.Error.CodexErrorInfo); err == nil && len(raw) > 0 && string(raw) != "null" {
			return string(raw)
		}
	}
	return e.Code
}

type ThreadStartedEvent struct {
	ThreadID string  `json:"threadId,omitempty"`
	Thread   *Thread `json:"thread,omitempty"`
}

type ThreadArchivedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
}

type ThreadUnarchivedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
}

type ThreadClosedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
}

type ThreadStatusChangedEvent struct {
	ThreadID string       `json:"threadId,omitempty"`
	Status   ThreadStatus `json:"status,omitempty"`
}

type ThreadGoalUpdatedEvent struct {
	ThreadID string                  `json:"threadId,omitempty"`
	Goal     *schematypes.ThreadGoal `json:"goal,omitempty"`
}

type ThreadGoalClearedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
}

type ServerRequestResolvedEvent struct {
	ThreadID  string          `json:"threadId,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}
