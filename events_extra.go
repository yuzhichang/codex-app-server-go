package codexgo

import (
	"encoding/json"
	"reflect"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

// This file defines typed events for the "extra" server-push notifications added
// in the notification-coverage phase. Complex nested payloads (settings, rate
// limits, hook runs, response items, audio chunks, etc.) are kept as
// json.RawMessage pass-through rather than fully modeled.

// --- NP1: thread & account lifecycle ---

type ThreadDeletedEvent struct {
	ThreadID string `json:"threadId"`
}

type ThreadNameUpdatedEvent struct {
	ThreadID   string `json:"threadId"`
	ThreadName string `json:"threadName,omitempty"`
}

// ThreadCompactedEvent is the thread/compacted (context compaction) notification.
type ThreadCompactedEvent struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type ThreadSettingsUpdatedEvent struct {
	ThreadID       string          `json:"threadId"`
	ThreadSettings json.RawMessage `json:"threadSettings,omitempty"`
}

type AccountUpdatedEvent struct {
	AuthMode string `json:"authMode,omitempty"`
	PlanType string `json:"planType,omitempty"`
}

type AccountRateLimitsUpdatedEvent struct {
	RateLimits json.RawMessage `json:"rateLimits,omitempty"`
}

// --- NP2: warnings & model diagnostics ---

type WarningEvent struct {
	ThreadID string `json:"threadId,omitempty"`
	Message  string `json:"message"`
}

type ConfigWarningEvent struct {
	Summary string          `json:"summary"`
	Details string          `json:"details,omitempty"`
	Path    string          `json:"path,omitempty"`
	Range   json.RawMessage `json:"range,omitempty"`
}

type DeprecationNoticeEvent struct {
	Summary string `json:"summary"`
	Details string `json:"details,omitempty"`
}

type GuardianWarningEvent struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type WindowsWorldWritableWarningEvent struct {
	SamplePaths []string `json:"samplePaths,omitempty"`
	ExtraCount  int      `json:"extraCount,omitempty"`
	FailedScan  bool     `json:"failedScan,omitempty"`
}

type WindowsSandboxSetupCompletedEvent struct {
	Mode    string `json:"mode,omitempty"`
	Success bool   `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ModelReroutedEvent struct {
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	FromModel string          `json:"fromModel"`
	ToModel   string          `json:"toModel"`
	Reason    json.RawMessage `json:"reason,omitempty"`
}

type ModelVerificationEvent struct {
	ThreadID      string          `json:"threadId"`
	TurnID        string          `json:"turnId"`
	Verifications json.RawMessage `json:"verifications,omitempty"`
}

type TurnModerationMetadataEvent struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// --- NP3: process / exec / hooks ---

type ProcessExitedEvent struct {
	ProcessHandle    string `json:"processHandle"`
	ExitCode         int    `json:"exitCode"`
	Stdout           string `json:"stdout,omitempty"`
	StdoutCapReached bool   `json:"stdoutCapReached,omitempty"`
	Stderr           string `json:"stderr,omitempty"`
	StderrCapReached bool   `json:"stderrCapReached,omitempty"`
}

type ProcessOutputDeltaEvent struct {
	ProcessHandle string `json:"processHandle"`
	Stream        string `json:"stream,omitempty"`
	DeltaBase64   string `json:"deltaBase64,omitempty"`
	CapReached    bool   `json:"capReached,omitempty"`
}

type TerminalInteractionEvent struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	ProcessID string `json:"processId"`
	Stdin     string `json:"stdin,omitempty"`
}

type McpToolCallProgressEvent struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Message  string `json:"message,omitempty"`
}

type HookStartedEvent struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId,omitempty"`
	Run      json.RawMessage `json:"run,omitempty"`
}

type HookCompletedEvent struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId,omitempty"`
	Run      json.RawMessage `json:"run,omitempty"`
}

type RawResponseItemCompletedEvent struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Item     json.RawMessage `json:"item,omitempty"`
}

// --- NP4: subsystem status ---

type McpServerStatusUpdatedEvent struct {
	ThreadID string `json:"threadId,omitempty"`
	Name     string `json:"name"`
	Status   string `json:"status,omitempty"`
	Error    string `json:"error,omitempty"`
}

type McpServerOauthLoginCompletedEvent struct {
	Name    string `json:"name"`
	Success bool   `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

type SkillsChangedEvent struct {
	Cursor       string `json:"cursor,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	ThreadID     string `json:"threadId,omitempty"`
	ForceRefetch bool   `json:"forceRefetch,omitempty"`
}

type FsChangedEvent struct {
	WatchID      string   `json:"watchId"`
	ChangedPaths []string `json:"changedPaths,omitempty"`
}

type AppListUpdatedEvent struct {
	Data json.RawMessage `json:"data,omitempty"`
}

type RemoteControlStatusChangedEvent struct {
	Status         string `json:"status,omitempty"`
	ServerName     string `json:"serverName,omitempty"`
	InstallationID string `json:"installationId,omitempty"`
	EnvironmentID  string `json:"environmentId,omitempty"`
}

type ExternalAgentConfigImportCompletedEvent struct {
	ImportID        string          `json:"importId"`
	ItemTypeResults json.RawMessage `json:"itemTypeResults,omitempty"`
}

type ExternalAgentConfigImportProgressEvent struct {
	ImportID        string          `json:"importId"`
	ItemTypeResults json.RawMessage `json:"itemTypeResults,omitempty"`
}

// --- NP5: realtime (voice/audio) ---

type ThreadRealtimeStartedEvent struct {
	ThreadID          string `json:"threadId"`
	RealtimeSessionID string `json:"realtimeSessionId,omitempty"`
	Version           string `json:"version,omitempty"`
}

type ThreadRealtimeClosedEvent struct {
	ThreadID string `json:"threadId"`
	Reason   string `json:"reason,omitempty"`
}

type ThreadRealtimeErrorEvent struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type ThreadRealtimeItemAddedEvent struct {
	ThreadID string          `json:"threadId"`
	Item     json.RawMessage `json:"item,omitempty"`
}

type ThreadRealtimeSdpEvent struct {
	ThreadID string `json:"threadId"`
	SDP      string `json:"sdp"`
}

type ThreadRealtimeOutputAudioDeltaEvent struct {
	ThreadID string          `json:"threadId"`
	Audio    json.RawMessage `json:"audio,omitempty"`
}

type ThreadRealtimeTranscriptDeltaEvent struct {
	ThreadID string `json:"threadId"`
	Role     string `json:"role,omitempty"`
	Delta    string `json:"delta,omitempty"`
}

type ThreadRealtimeTranscriptDoneEvent struct {
	ThreadID string `json:"threadId"`
	Role     string `json:"role,omitempty"`
	Text     string `json:"text,omitempty"`
}

// extraEventTarget returns a pointer to the typed event struct for the given
// notification method, or nil if it is not one of the "extra" notifications.
func extraEventTarget(method string) any {
	switch method {
	// NP1
	case protocol.MethodThreadDeleted:
		return &ThreadDeletedEvent{}
	case protocol.MethodThreadNameUpdated:
		return &ThreadNameUpdatedEvent{}
	case protocol.MethodThreadCompacted:
		return &ThreadCompactedEvent{}
	case protocol.MethodThreadSettingsUpdated:
		return &ThreadSettingsUpdatedEvent{}
	case protocol.MethodAccountUpdated:
		return &AccountUpdatedEvent{}
	case protocol.MethodAccountRateLimitsUpdated:
		return &AccountRateLimitsUpdatedEvent{}
	// NP2
	case protocol.MethodWarning:
		return &WarningEvent{}
	case protocol.MethodConfigWarning:
		return &ConfigWarningEvent{}
	case protocol.MethodDeprecationNotice:
		return &DeprecationNoticeEvent{}
	case protocol.MethodGuardianWarning:
		return &GuardianWarningEvent{}
	case protocol.MethodWindowsWorldWritableWarning:
		return &WindowsWorldWritableWarningEvent{}
	case protocol.MethodWindowsSandboxSetupCompleted:
		return &WindowsSandboxSetupCompletedEvent{}
	case protocol.MethodModelRerouted:
		return &ModelReroutedEvent{}
	case protocol.MethodModelVerification:
		return &ModelVerificationEvent{}
	case protocol.MethodTurnModerationMetadata:
		return &TurnModerationMetadataEvent{}
	// NP3
	case protocol.MethodProcessExited:
		return &ProcessExitedEvent{}
	case protocol.MethodProcessOutputDelta:
		return &ProcessOutputDeltaEvent{}
	case protocol.MethodItemTerminalInteraction:
		return &TerminalInteractionEvent{}
	case protocol.MethodItemMcpToolCallProgress:
		return &McpToolCallProgressEvent{}
	case protocol.MethodHookStarted:
		return &HookStartedEvent{}
	case protocol.MethodHookCompleted:
		return &HookCompletedEvent{}
	case protocol.MethodRawResponseItemCompleted:
		return &RawResponseItemCompletedEvent{}
	// NP4
	case protocol.MethodMcpServerStatusUpdated:
		return &McpServerStatusUpdatedEvent{}
	case protocol.MethodMcpServerOauthLoginCompleted:
		return &McpServerOauthLoginCompletedEvent{}
	case protocol.MethodSkillsChanged:
		return &SkillsChangedEvent{}
	case protocol.MethodFsChanged:
		return &FsChangedEvent{}
	case protocol.MethodAppListUpdated:
		return &AppListUpdatedEvent{}
	case protocol.MethodRemoteControlStatusChanged:
		return &RemoteControlStatusChangedEvent{}
	case protocol.MethodExternalAgentConfigImportDone:
		return &ExternalAgentConfigImportCompletedEvent{}
	case protocol.MethodExternalAgentConfigImportProg:
		return &ExternalAgentConfigImportProgressEvent{}
	// NP5
	case protocol.MethodThreadRealtimeStarted:
		return &ThreadRealtimeStartedEvent{}
	case protocol.MethodThreadRealtimeClosed:
		return &ThreadRealtimeClosedEvent{}
	case protocol.MethodThreadRealtimeError:
		return &ThreadRealtimeErrorEvent{}
	case protocol.MethodThreadRealtimeItemAdded:
		return &ThreadRealtimeItemAddedEvent{}
	case protocol.MethodThreadRealtimeSdp:
		return &ThreadRealtimeSdpEvent{}
	case protocol.MethodThreadRealtimeOutputAudio:
		return &ThreadRealtimeOutputAudioDeltaEvent{}
	case protocol.MethodThreadRealtimeTranscriptDelta:
		return &ThreadRealtimeTranscriptDeltaEvent{}
	case protocol.MethodThreadRealtimeTranscriptDone:
		return &ThreadRealtimeTranscriptDoneEvent{}
	// NP6 — formerly unimplemented stable notifications.
	case protocol.MethodThreadReverted:
		return &ThreadRevertedEvent{}
	case protocol.MethodThreadAttachmentUpdated:
		return &ThreadAttachmentUpdatedEvent{}
	case protocol.MethodGatewayOAuthChanged:
		return &AccountGatewayOAuthChangedEvent{}
	case protocol.MethodFuzzyFileSearchSessionUpdated:
		return &FuzzyFileSearchSessionUpdatedEvent{}
	case protocol.MethodFuzzyFileSearchSessionCompleted:
		return &FuzzyFileSearchSessionCompletedEvent{}
	case protocol.MethodModelSafetyBufferingUpdated:
		return &ModelSafetyBufferingUpdatedEvent{}
	// These two share one payload type: AuthRecoveryNotification carries no
	// started/completed discriminator, so the method name is the only signal.
	case protocol.MethodAuthRecoveryStarted:
		return &ModelProviderAuthRecoveryStartedEvent{}
	case protocol.MethodAuthRecoveryCompleted:
		return &ModelProviderAuthRecoveryCompletedEvent{}
	default:
		return nil
	}
}

// derefExtraEvent returns the dereferenced value for an extra event pointer.
//
// This is deliberately generic. It used to be a hand-maintained type switch listing every
// event type, which meant each new event needed a second edit here; forgetting it made the
// event's Value nil and panicked the consumer. `extraEventTarget` only ever produces
// pointers to event structs, so dereferencing any non-nil pointer is correct.
func derefExtraEvent(v any) (any, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, false
	}
	return rv.Elem().Interface(), true
}
