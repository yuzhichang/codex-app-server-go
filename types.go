// Package codexgo provides a compact client for the Codex app-server.
package codexgo

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
	"github.com/zealbase/codex-app-server-go/internal/transport"
)

type (
	ClientInfo             = schematypes.ClientInfo
	InitializeCapabilities = schematypes.InitializeCapabilities
	InitializeParams       = schematypes.InitializeParams
	InitializeResponse     = schematypes.InitializeResponse
	ThreadStartParams      = schematypes.ThreadStartParams
	ThreadResumeParams     = schematypes.ThreadResumeParams
	ThreadReadParams       = schematypes.ThreadReadParams
	TurnStartParams        = schematypes.TurnStartParams
	TurnInterruptParams    = schematypes.TurnInterruptParams
	ThreadForkParams       = schematypes.ThreadForkParams
	ThreadListParams       = schematypes.ThreadListParams
	ThreadArchiveParams    = schematypes.ThreadArchiveParams
	ThreadUnarchiveParams  = schematypes.ThreadUnarchiveParams
	ThreadSetNameParams    = schematypes.ThreadSetNameParams
	ThreadRevertParams     = schematypes.ThreadRevertParams
	ThreadRevertResponse   = schematypes.ThreadRevertResponse
	TurnSteerParams        = schematypes.TurnSteerParams
	ReviewStartParams      = schematypes.ReviewStartParams
	Thread                 = protocol.Thread
	Turn                   = protocol.Turn
	Item                   = protocol.Item
	ThreadStatus           = protocol.ThreadStatus
	ThreadActiveFlag       = protocol.ThreadActiveFlag
	TurnStatus             = protocol.TurnStatus
	TurnItemsView          = protocol.TurnItemsView
	ItemKind               = protocol.ItemKind
	ThreadTokenUsage       = protocol.ThreadTokenUsage
	TokenUsageBreakdown    = protocol.TokenUsageBreakdown
	TurnError              = protocol.TurnError

	// Filesystem RPC types (fs/*).
	AbsolutePathBuf           = schematypes.AbsolutePathBuf
	FsReadFileParams          = schematypes.FsReadFileParams
	FsReadFileResponse        = schematypes.FsReadFileResponse
	FsWriteFileParams         = schematypes.FsWriteFileParams
	FsWriteFileResponse       = schematypes.FsWriteFileResponse
	FsCreateDirectoryParams   = schematypes.FsCreateDirectoryParams
	FsCreateDirectoryResponse = schematypes.FsCreateDirectoryResponse
	FsGetMetadataParams       = schematypes.FsGetMetadataParams
	FsGetMetadataResponse     = schematypes.FsGetMetadataResponse
	FsReadDirectoryParams     = schematypes.FsReadDirectoryParams
	FsReadDirectoryResponse   = schematypes.FsReadDirectoryResponse
	FsReadDirectoryEntry      = schematypes.FsReadDirectoryEntry
	FsRemoveParams            = schematypes.FsRemoveParams
	FsRemoveResponse          = schematypes.FsRemoveResponse
	FsCopyParams              = schematypes.FsCopyParams
	FsCopyResponse            = schematypes.FsCopyResponse
	FsWatchParams             = schematypes.FsWatchParams
	FsWatchResponse           = schematypes.FsWatchResponse
	FsUnwatchParams           = schematypes.FsUnwatchParams
	FsUnwatchResponse         = schematypes.FsUnwatchResponse
	// NOTE: FsChangedEvent already exists in events_extra.go (it is one of the
	// notifications wired before the fs/* RPCs were). Do not re-alias it here.

	// MCP lifecycle RPC types (mcpServer/*). Names follow upstream exactly --
	// `ListMcpServerStatusParams` and `McpResourceReadParams` keep their upstream
	// prefixes rather than being renamed for local taste.
	McpServerOauthLoginParams        = schematypes.McpServerOauthLoginParams
	McpServerOauthLoginResponse      = schematypes.McpServerOauthLoginResponse
	ListMcpServerStatusParams        = schematypes.ListMcpServerStatusParams
	ListMcpServerStatusResponse      = schematypes.ListMcpServerStatusResponse
	McpServerStatus                  = schematypes.McpServerStatus
	McpServerStatusDetail            = schematypes.McpServerStatusDetail
	McpResourceReadParams            = schematypes.McpResourceReadParams
	McpResourceReadResponse          = schematypes.McpResourceReadResponse
	McpServerToolCallParams          = schematypes.McpServerToolCallParams
	McpServerToolCallResponse        = schematypes.McpServerToolCallResponse
	McpServerRefreshResponse         = schematypes.McpServerRefreshResponse
	McpServerStartupState            = schematypes.McpServerStartupState
	McpServerStartupFailureReason    = schematypes.McpServerStartupFailureReason
	McpAuthStatus                    = schematypes.McpAuthStatus
	McpServerConnectionStatus        = schematypes.McpServerConnectionStatus
	McpServerOauthClientRegistration = schematypes.McpServerOauthClientRegistration
	McpToolCallResult                = schematypes.McpToolCallResult

	// Notification payload types (decoder targets).
	ThreadRevertedEvent                  = schematypes.ThreadRevertedNotification
	ThreadAttachmentUpdatedEvent         = schematypes.ThreadAttachmentUpdatedNotification
	AccountGatewayOAuthChangedEvent      = schematypes.GatewayOAuthChangedNotification
	FuzzyFileSearchSessionUpdatedEvent   = schematypes.FuzzyFileSearchSessionUpdatedNotification
	FuzzyFileSearchSessionCompletedEvent = schematypes.FuzzyFileSearchSessionCompletedNotification
	ModelSafetyBufferingUpdatedEvent     = schematypes.ModelSafetyBufferingUpdatedNotification

	// Both auth-recovery notifications share ONE payload type, which carries no
	// started/completed discriminator (message/provider/threadId/turnId only). The wire
	// method name is the only way to tell the two events apart, which is why both aliases
	// below point at the same Go type.
	ModelProviderAuthRecoveryStartedEvent   = schematypes.AuthRecoveryNotification
	ModelProviderAuthRecoveryCompletedEvent = schematypes.AuthRecoveryNotification

	// Config RPC types. Upstream names, replacing the SDK's former hand-written
	// ConfigReadRequest / ConfigReadResult / ConfigValueWriteRequest / ConfigBatchWriteRequest.
	Config                 = schematypes.Config
	ConfigReadParams       = schematypes.ConfigReadParams
	ConfigReadResponse     = schematypes.ConfigReadResponse
	ConfigLayerMetadata    = schematypes.ConfigLayerMetadata
	ConfigValueWriteParams = schematypes.ConfigValueWriteParams
	ConfigBatchWriteParams = schematypes.ConfigBatchWriteParams
	ConfigWriteResponse    = schematypes.ConfigWriteResponse
	MergeStrategy          = schematypes.MergeStrategy
	// NOTE: McpServerOauthLoginCompletedEvent and McpServerStatusUpdatedEvent already
	// exist in events_extra.go. Do not re-alias them here.

	// Plugin / marketplace RPC types.
	MarketplaceAddParams             = schematypes.MarketplaceAddParams
	MarketplaceAddResponse           = schematypes.MarketplaceAddResponse
	MarketplaceRemoveParams          = schematypes.MarketplaceRemoveParams
	MarketplaceRemoveResponse        = schematypes.MarketplaceRemoveResponse
	MarketplaceUpgradeParams         = schematypes.MarketplaceUpgradeParams
	MarketplaceUpgradeResponse       = schematypes.MarketplaceUpgradeResponse
	PluginListParams                 = schematypes.PluginListParams
	PluginListResponse               = schematypes.PluginListResponse
	PluginInstalledParams            = schematypes.PluginInstalledParams
	PluginInstalledResponse          = schematypes.PluginInstalledResponse
	PluginReconcileParams            = schematypes.PluginReconcileParams
	PluginReconcileResponse          = schematypes.PluginReconcileResponse
	PluginReadParams                 = schematypes.PluginReadParams
	PluginReadResponse               = schematypes.PluginReadResponse
	PluginSkillReadParams            = schematypes.PluginSkillReadParams
	PluginSkillReadResponse          = schematypes.PluginSkillReadResponse
	PluginShareUpdateDiscoverability = schematypes.PluginShareUpdateDiscoverability
	PluginSummary                    = schematypes.PluginSummary
	PluginDetail                     = schematypes.PluginDetail
	PluginShareSaveParams            = schematypes.PluginShareSaveParams
	PluginShareSaveResponse          = schematypes.PluginShareSaveResponse
	PluginShareUpdateTargetsParams   = schematypes.PluginShareUpdateTargetsParams
	PluginShareUpdateTargetsResponse = schematypes.PluginShareUpdateTargetsResponse
	PluginShareListParams            = schematypes.PluginShareListParams
	PluginShareListResponse          = schematypes.PluginShareListResponse
	PluginShareCheckoutParams        = schematypes.PluginShareCheckoutParams
	PluginShareCheckoutResponse      = schematypes.PluginShareCheckoutResponse
	PluginShareDeleteParams          = schematypes.PluginShareDeleteParams
	PluginShareDeleteResponse        = schematypes.PluginShareDeleteResponse
	PluginShareTarget                = schematypes.PluginShareTarget
	PluginInstallParams              = schematypes.PluginInstallParams
	PluginInstallResponse            = schematypes.PluginInstallResponse
	PluginUninstallParams            = schematypes.PluginUninstallParams
	PluginUninstallResponse          = schematypes.PluginUninstallResponse

	// App registry RPC types. Upstream prefixes these with "Apps", not "App".
	AppsListParams        = schematypes.AppsListParams
	AppsListResponse      = schematypes.AppsListResponse
	AppsInstalledParams   = schematypes.AppsInstalledParams
	AppsInstalledResponse = schematypes.AppsInstalledResponse
	AppsReadParams        = schematypes.AppsReadParams
	AppsReadResponse      = schematypes.AppsReadResponse
)

const (
	ThreadStatusNotLoaded   = protocol.ThreadStatusNotLoaded
	ThreadStatusIdle        = protocol.ThreadStatusIdle
	ThreadStatusSystemError = protocol.ThreadStatusSystemError
	ThreadStatusActive      = protocol.ThreadStatusActive
)

const (
	ThreadActiveFlagWaitingOnApproval  = protocol.ThreadActiveFlagWaitingOnApproval
	ThreadActiveFlagWaitingOnUserInput = protocol.ThreadActiveFlagWaitingOnUserInput
)

// MCP enum values. A type alias does not carry its constants, so every enum the SDK
// re-exports needs its values re-exported here too.
const (
	McpAuthStatusUnknown     = schematypes.McpAuthStatusUnknown
	McpAuthStatusUnsupported = schematypes.McpAuthStatusUnsupported
	McpAuthStatusNotLoggedIn = schematypes.McpAuthStatusNotLoggedIn
	McpAuthStatusBearerToken = schematypes.McpAuthStatusBearerToken
	McpAuthStatusOAuth       = schematypes.McpAuthStatusOAuth

	McpServerStartupStateStarting  = schematypes.McpServerStartupStateStarting
	McpServerStartupStateReady     = schematypes.McpServerStartupStateReady
	McpServerStartupStateFailed    = schematypes.McpServerStartupStateFailed
	McpServerStartupStateCancelled = schematypes.McpServerStartupStateCancelled

	McpServerStatusDetailFull             = schematypes.McpServerStatusDetailFull
	McpServerStatusDetailToolsAndAuthOnly = schematypes.McpServerStatusDetailToolsAndAuthOnly

	McpServerConnectionStatusNotStarted             = schematypes.McpServerConnectionStatusNotStarted
	McpServerConnectionStatusStarting               = schematypes.McpServerConnectionStatusStarting
	McpServerConnectionStatusConnected              = schematypes.McpServerConnectionStatusConnected
	McpServerConnectionStatusAuthenticationRequired = schematypes.McpServerConnectionStatusAuthenticationRequired
	McpServerConnectionStatusFailed                 = schematypes.McpServerConnectionStatusFailed
	McpServerConnectionStatusCancelled              = schematypes.McpServerConnectionStatusCancelled
	McpServerConnectionStatusDisabled               = schematypes.McpServerConnectionStatusDisabled

	// Plugin sharing enum values. Note the wire values are SCREAMING_CASE upstream, so the
	// constants keep that shape rather than being re-cased locally.
	PluginShareUpdateDiscoverabilityUNLISTED = schematypes.PluginShareUpdateDiscoverabilityUNLISTED

	// Config merge strategies (upstream enum values).
	MergeStrategyReplace = schematypes.MergeStrategyReplace
	MergeStrategyUpsert  = schematypes.MergeStrategyUpsert

	// MCP elicitation enum values. A type alias does not carry its constants, so the
	// action and mode values have to be re-exported explicitly.
	McpServerElicitationActionAccept  = protocol.McpServerElicitationActionAccept
	McpServerElicitationActionDecline = protocol.McpServerElicitationActionDecline
	McpServerElicitationActionCancel  = protocol.McpServerElicitationActionCancel

	McpServerElicitationModeForm             = protocol.McpServerElicitationModeForm
	McpServerElicitationModeOpenAIForm       = protocol.McpServerElicitationModeOpenAIForm
	McpServerElicitationModeUserVerification = protocol.McpServerElicitationModeUserVerification

	// ChatGPT auth token refresh reasons (server -> client request).
	ChatgptAuthTokensRefreshReasonUnauthorized = protocol.ChatgptAuthTokensRefreshReasonUnauthorized

	PluginShareUpdateDiscoverabilityPRIVATE = schematypes.PluginShareUpdateDiscoverabilityPRIVATE
	PluginShareUpdateDiscoverabilityLISTED  = schematypes.PluginShareUpdateDiscoverabilityLISTED
)

const (
	TurnStatusCompleted   = protocol.TurnStatusCompleted
	TurnStatusInterrupted = protocol.TurnStatusInterrupted
	TurnStatusFailed      = protocol.TurnStatusFailed
	TurnStatusInProgress  = protocol.TurnStatusInProgress
)

const (
	TurnItemsViewNotLoaded = protocol.TurnItemsViewNotLoaded
	TurnItemsViewSummary   = protocol.TurnItemsViewSummary
	TurnItemsViewFull      = protocol.TurnItemsViewFull
)

const (
	ItemKindUserMessage         = protocol.ItemKindUserMessage
	ItemKindHookPrompt          = protocol.ItemKindHookPrompt
	ItemKindAgentMessage        = protocol.ItemKindAgentMessage
	ItemKindPlan                = protocol.ItemKindPlan
	ItemKindReasoning           = protocol.ItemKindReasoning
	ItemKindCommandExecution    = protocol.ItemKindCommandExecution
	ItemKindFileChange          = protocol.ItemKindFileChange
	ItemKindMCPToolCall         = protocol.ItemKindMCPToolCall
	ItemKindDynamicToolCall     = protocol.ItemKindDynamicToolCall
	ItemKindCollabAgentToolCall = protocol.ItemKindCollabAgentToolCall
	ItemKindSubAgentActivity    = protocol.ItemKindSubAgentActivity
	ItemKindWebSearch           = protocol.ItemKindWebSearch
	ItemKindImageView           = protocol.ItemKindImageView
	ItemKindSleep               = protocol.ItemKindSleep
	ItemKindImageGeneration     = protocol.ItemKindImageGeneration
	ItemKindEnteredReviewMode   = protocol.ItemKindEnteredReviewMode
	ItemKindExitedReviewMode    = protocol.ItemKindExitedReviewMode
	ItemKindContextCompaction   = protocol.ItemKindContextCompaction
)

// ApprovalMode is a typed approval policy for turns and threads. The string
// values match the wire values the app-server expects for approvalPolicy.
type ApprovalMode string

const (
	ApprovalModeDenyAll    ApprovalMode = "deny_all"
	ApprovalModeAutoReview ApprovalMode = "auto_review"
	ApprovalModeOnRequest  ApprovalMode = "on-request"
	ApprovalModeNever      ApprovalMode = "never"
)

// SandboxMode is a typed sandbox policy for turns. The string values match the
// wire values the app-server expects for sandboxPolicy.
type SandboxMode string

const (
	SandboxReadOnly       SandboxMode = "read-only"
	SandboxWorkspaceWrite SandboxMode = "workspace-write"
	SandboxFullAccess     SandboxMode = "danger-full-access"
)

// CodexError wraps an RPCError and exposes the structured codexErrorInfo payload.
type CodexError struct {
	RPCCode        int
	RPCMessage     string
	ErrorType      string // codexErrorInfo.type  e.g. "HttpConnectionFailed"
	HTTPStatusCode int    // codexErrorInfo.httpStatusCode e.g. 429
	AdditionalInfo string // additionalDetails
}

func (e *CodexError) Error() string {
	if e.HTTPStatusCode != 0 {
		return fmt.Sprintf("codex error %d (HTTP %d): %s", e.RPCCode, e.HTTPStatusCode, e.RPCMessage)
	}
	return fmt.Sprintf("codex error %d: %s", e.RPCCode, e.RPCMessage)
}

// AsCodexError unwraps err into a *CodexError if the underlying cause is a
// transport.RPCError that carries a codexErrorInfo payload.
func AsCodexError(err error) (*CodexError, bool) {
	var rpcErr *transport.RPCError
	if !errors.As(err, &rpcErr) {
		return nil, false
	}
	ce := &CodexError{RPCCode: rpcErr.Code, RPCMessage: rpcErr.Message}
	if len(rpcErr.Data) > 0 {
		var envelope struct {
			CodexErrorInfo struct {
				Type           string `json:"type"`
				HTTPStatusCode int    `json:"httpStatusCode"`
			} `json:"codexErrorInfo"`
			AdditionalDetails string `json:"additionalDetails"`
		}
		if json.Unmarshal(rpcErr.Data, &envelope) == nil {
			ce.ErrorType = envelope.CodexErrorInfo.Type
			ce.HTTPStatusCode = envelope.CodexErrorInfo.HTTPStatusCode
			ce.AdditionalInfo = envelope.AdditionalDetails
		}
	}
	return ce, true
}

// IsRateLimited reports whether err is an HTTP 429 rate-limit error from Codex.
func IsRateLimited(err error) bool {
	ce, ok := AsCodexError(err)
	return ok && ce.HTTPStatusCode == 429
}

// IsUnauthorized reports whether err is an HTTP 401 authentication error.
func IsUnauthorized(err error) bool {
	ce, ok := AsCodexError(err)
	return ok && ce.HTTPStatusCode == 401
}

// IsInternalServerError reports whether err is an HTTP 500 from Codex.
func IsInternalServerError(err error) bool {
	ce, ok := AsCodexError(err)
	return ok && ce.HTTPStatusCode == 500
}

// IsHttpConnectionFailed reports whether err is a connection failure.
func IsHttpConnectionFailed(err error) bool {
	ce, ok := AsCodexError(err)
	return ok && ce.ErrorType == "HttpConnectionFailed"
}

// Package codexgo provides a compact client for the Codex app-server.
