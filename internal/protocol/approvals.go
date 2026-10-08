package protocol

import (
	"encoding/json"

	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
)

// Approval decision enum aliases -- canonical definitions live in the generated schema package.

type CommandExecutionApprovalDecision = schematypes.CommandExecutionApprovalDecision
type FileChangeApprovalDecision = schematypes.FileChangeApprovalDecision
type PermissionGrantScope = schematypes.PermissionGrantScope

const (
	CommandExecutionApprovalDecisionAccept                        = schematypes.CommandExecutionApprovalDecisionAccept
	CommandExecutionApprovalDecisionAcceptForSession              = schematypes.CommandExecutionApprovalDecisionAcceptForSession
	CommandExecutionApprovalDecisionAcceptWithExecpolicyAmendment = schematypes.CommandExecutionApprovalDecisionAcceptWithExecpolicyAmendment
	CommandExecutionApprovalDecisionApplyNetworkPolicyAmendment   = schematypes.CommandExecutionApprovalDecisionApplyNetworkPolicyAmendment
	CommandExecutionApprovalDecisionDecline                       = schematypes.CommandExecutionApprovalDecisionDecline
	CommandExecutionApprovalDecisionCancel                        = schematypes.CommandExecutionApprovalDecisionCancel
)

const (
	FileChangeApprovalDecisionAccept           = schematypes.FileChangeApprovalDecisionAccept
	FileChangeApprovalDecisionAcceptForSession = schematypes.FileChangeApprovalDecisionAcceptForSession
	FileChangeApprovalDecisionDecline          = schematypes.FileChangeApprovalDecisionDecline
	FileChangeApprovalDecisionCancel           = schematypes.FileChangeApprovalDecisionCancel
)

const (
	PermissionGrantScopeSession = schematypes.PermissionGrantScopeSession
	PermissionGrantScopeTurn    = schematypes.PermissionGrantScopeTurn
)

// Approval request/response types (not in the protocol schema definition).

type CommandExecutionApprovalRequest struct {
	ItemID                 string          `json:"itemId,omitempty"`
	ThreadID               string          `json:"threadId,omitempty"`
	TurnID                 string          `json:"turnId,omitempty"`
	EnvironmentID          string          `json:"environmentId,omitempty"`
	ApprovalID             string          `json:"approvalId,omitempty"`
	Reason                 string          `json:"reason,omitempty"`
	Command                string          `json:"command,omitempty"`
	Cwd                    string          `json:"cwd,omitempty"`
	CommandActions         []string        `json:"commandActions,omitempty"`
	NetworkApprovalContext json.RawMessage `json:"networkApprovalContext,omitempty"`
}

type CommandExecutionApprovalResponse struct {
	Decision CommandExecutionApprovalDecision `json:"decision"`
}

type FileChangeApprovalRequest struct {
	ItemID    string   `json:"itemId,omitempty"`
	ThreadID  string   `json:"threadId,omitempty"`
	TurnID    string   `json:"turnId,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	GrantRoot string   `json:"grantRoot,omitempty"`
	FilePaths []string `json:"filePaths,omitempty"`
	Diff      string   `json:"diff,omitempty"`
}

type FileChangeApprovalResponse struct {
	Decision FileChangeApprovalDecision `json:"decision"`
}

// PermissionsApprovalRequest is the payload of `item/permissions/requestApproval`.
//
// Declared upstream as `PermissionsRequestApprovalParams`
// (protocol/v2/permissions.rs:771-786) and absent from the v2 aggregate.
//
// The previous SDK shape typed `permissions` as `[]string`. That was not merely imprecise:
// upstream sends an OBJECT (`RequestPermissionProfile`), so decoding a real request into a
// []string fails outright. The field is now passed through as raw JSON rather than
// modelled, because the profile is a family of nested types (network + fileSystem with
// per-path read/write lists) that the SDK has no other use for; a handler can decode it if
// it needs the detail.
//
// `startedAtMs` and `cwd` are required upstream and were missing entirely.
type PermissionsApprovalRequest struct {
	ThreadID      string          `json:"threadId"`
	TurnID        string          `json:"turnId"`
	ItemID        string          `json:"itemId"`
	EnvironmentID string          `json:"environmentId,omitempty"`
	StartedAtMs   int64           `json:"startedAtMs"`
	CWD           string          `json:"cwd"`
	Reason        string          `json:"reason,omitempty"`
	Permissions   json.RawMessage `json:"permissions"`
}

// GrantedPermissionProfile is the *additional* permission being granted on top of the
// sandbox (protocol/v2/permissions.rs:505-512). Both fields are optional upstream, so the
// zero value grants nothing.
type GrantedPermissionProfile struct {
	// Network and FileSystem are pass-through for the same reason as above: the SDK never
	// needs to inspect them, only to relay what a handler decided.
	Network    json.RawMessage `json:"network,omitempty"`
	FileSystem json.RawMessage `json:"fileSystem,omitempty"`
}

// PermissionsApprovalResponse answers `item/permissions/requestApproval`.
//
// Upstream `PermissionsRequestApprovalResponse` (permissions.rs:796-807): `permissions` is
// required, while `scope` defaults to `turn` and `strictAutoReview` is optional -- both are
// omitted by the SDK's default answer, which sidesteps the scope enum's wire casing.
type PermissionsApprovalResponse struct {
	Permissions      GrantedPermissionProfile `json:"permissions"`
	Scope            string                   `json:"scope,omitempty"`
	StrictAutoReview *bool                    `json:"strictAutoReview,omitempty"`
}

// DenyPermissions is the answer when no handler is configured: grant nothing.
//
// Upstream's own test `permissions_request_approval_response_defaults_scope_to_turn`
// deserializes exactly `{"permissions": {}}`, so an empty profile is a valid -- and
// non-escalating -- reply.
func DenyPermissions() PermissionsApprovalResponse {
	return PermissionsApprovalResponse{Permissions: GrantedPermissionProfile{}}
}

type UserInputOption struct {
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type UserInputQuestion struct {
	Header   string            `json:"header,omitempty"`
	ID       string            `json:"id,omitempty"`
	Question string            `json:"question,omitempty"`
	Options  []UserInputOption `json:"options,omitempty"`
}

type UserInputRequest struct {
	ItemID    string              `json:"itemId,omitempty"`
	ThreadID  string              `json:"threadId,omitempty"`
	TurnID    string              `json:"turnId,omitempty"`
	Questions []UserInputQuestion `json:"questions,omitempty"`
}

// UserInputAnswer is one question's answer.
//
// Upstream `ToolRequestUserInputAnswer` (protocol/v2/item.rs:1798-1801) holds a LIST of
// strings, so `UserInputResponse.Answers` used to be typed `map[string]string` and could not
// represent a real answer -- nor produce one.
type UserInputAnswer struct {
	Answers []string `json:"answers"`
}

// UserInputResponse answers `item/tool/requestUserInput`.
//
// Upstream `ToolRequestUserInputResponse` (item.rs:1803-1809): `answers` is required, so the
// SDK's refusal is an empty map rather than an absent field.
type UserInputResponse struct {
	Answers map[string]UserInputAnswer `json:"answers"`
}

type UserInputResult = UserInputResponse

// DeclineUserInput is the answer when no handler is configured: answer nothing.
func DeclineUserInput() UserInputResponse {
	return UserInputResponse{Answers: map[string]UserInputAnswer{}}
}

type MCPServerElicitationRequest struct {
	ItemID   string          `json:"itemId,omitempty"`
	ThreadID string          `json:"threadId,omitempty"`
	TurnID   string          `json:"turnId,omitempty"`
	Kind     string          `json:"kind,omitempty"`
	Form     json.RawMessage `json:"form,omitempty"`
	URL      string          `json:"url,omitempty"`
}

// --- MCP server elicitation (upstream method `mcpServer/elicitation/request`) ---
//
// These types are deliberately hand-written rather than generated. The *method* is stable
// upstream, but `McpServerElicitationRequestParams.request` carries
// `#[experimental(nested)]`, so the whole Params/Response pair is omitted from the stable
// aggregate schema. Modelled from
// codex-rs/app-server-protocol/src/protocol/v2/mcp.rs (376-384, 787-830, 901-911).
//
// The upstream shape is a `mode`-tagged union flattened into the params. It is modelled
// here as one flat struct with an explicit Mode discriminator plus the union of every
// mode's fields. Rationale: no field is dropped and no input is rejected, at the cost of
// callers switching on Mode to know which fields are meaningful. The alternative -- a Go
// interface with a custom UnmarshalJSON -- would be stricter but would also reject any
// future mode this SDK has not been taught about, which for an elicitation prompt means
// hanging a live tool call.

// McpServerElicitationMode discriminates the flattened request.
type McpServerElicitationMode string

const (
	// McpServerElicitationModeForm is a schema-driven form (MCP `elicitation/create`).
	McpServerElicitationModeForm McpServerElicitationMode = "form"
	// McpServerElicitationModeOpenAIForm is the legacy OpenAI form extension.
	McpServerElicitationModeOpenAIForm McpServerElicitationMode = "openai/form"
	// McpServerElicitationModeUserVerification is a device-authenticated approval. It is
	// itself experimental upstream, so it is only ever seen if the client opted in.
	McpServerElicitationModeUserVerification McpServerElicitationMode = "openai/userVerification"
)

// McpServerElicitationRequestParams is a server-initiated request asking the client to
// collect user input on behalf of an MCP server.
type McpServerElicitationRequestParams struct {
	ThreadID   string                   `json:"threadId"`
	TurnID     string                   `json:"turnId,omitempty"`
	ServerName string                   `json:"serverName"`
	Mode       McpServerElicitationMode `json:"mode"`

	// Fields below are the flattened union; which apply depends on Mode.
	Meta            json.RawMessage `json:"_meta,omitempty"`
	Message         string          `json:"message,omitempty"`
	RequestedSchema json.RawMessage `json:"requestedSchema,omitempty"`
	// UserVerification mode only:
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Challenge   string `json:"challenge,omitempty"`
}

// McpServerElicitationAction is the client's answer to an elicitation.
type McpServerElicitationAction string

const (
	McpServerElicitationActionAccept  McpServerElicitationAction = "accept"
	McpServerElicitationActionDecline McpServerElicitationAction = "decline"
	McpServerElicitationActionCancel  McpServerElicitationAction = "cancel"
)

// McpServerElicitationRequestResponse answers an elicitation. Content carries the
// structured user input for accepted elicitations and is omitted for decline/cancel.
type McpServerElicitationRequestResponse struct {
	Action  McpServerElicitationAction `json:"action"`
	Content json.RawMessage            `json:"content,omitempty"`
	Meta    json.RawMessage            `json:"_meta,omitempty"`
}

// DeclineElicitation is the default answer when no handler is installed: the exchange is
// refused but the session continues (decision R9).
func DeclineElicitation() McpServerElicitationRequestResponse {
	return McpServerElicitationRequestResponse{Action: McpServerElicitationActionDecline}
}

// --- ChatGPT auth token refresh (upstream method `account/chatgptAuthTokens/refresh`) ---
//
// Hand-written for the same reason as the elicitation types: the definitions exist in
// codex-rs/app-server-protocol/src/protocol/v2/account.rs:271-299, but the stable aggregate
// does not carry them.
//
// This is a server -> client request: when Codex gets a 401 it asks the client to mint a
// fresh access token. It only arrives for clients that own the ChatGPT token lifecycle;
// the SDK's default handshake does not claim to.

// ChatgptAuthTokensRefreshReason explains why a refresh was requested.
type ChatgptAuthTokensRefreshReason string

const (
	// ChatgptAuthTokensRefreshReasonUnauthorized means the backend rejected the token (401).
	ChatgptAuthTokensRefreshReasonUnauthorized ChatgptAuthTokensRefreshReason = "unauthorized"
)

// ChatgptAuthTokensRefreshParams asks the client for a fresh access token.
type ChatgptAuthTokensRefreshParams struct {
	Reason ChatgptAuthTokensRefreshReason `json:"reason"`
	// PreviousAccountID is the workspace/account Codex was using, so a client managing
	// several accounts can refresh the right one. Empty when prior auth carried no account
	// id (upstream: null).
	PreviousAccountID string `json:"previousAccountId,omitempty"`
}

// ChatgptAuthTokensRefreshResponse carries the minted token back.
//
// AccessToken is a live credential. Upstream implements Debug by hand specifically to
// redact it (account.rs:301-309) -- do not log this struct verbatim.
type ChatgptAuthTokensRefreshResponse struct {
	AccessToken      string `json:"accessToken"`
	ChatgptAccountID string `json:"chatgptAccountId"`
	ChatgptPlanType  string `json:"chatgptPlanType,omitempty"`
}

// --- Legacy v1 approvals (`applyPatchApproval`, `execCommandApproval`) ---
//
// These two methods are declared stable upstream but deliberately not implemented as
// features (decision R4); the SDK still has to answer them, because leaving a server request
// unanswered can end the turn.
//
// The types are v1 (`protocol/v1.rs:156-180`) and absent from the v2 aggregate, so they are
// modelled from the exported schema (schema/json/ApplyPatchApprovalResponse.json).
//
// CRITICAL WIRE DETAIL: the decision we send is the `Denied` variant, which upstream declares
// as `Denied { rejection: String }` -- a STRUCT variant. With serde's default externally
// tagged representation that serializes to
//
//	{"denied": {"rejection": "denied"}}
//
// NOT to the bare string "denied". The bare string looks right from the Rust enum's
// `rename_all = "snake_case"` but would be rejected: no *unit* variant named `denied` exists,
// because the variant carries a payload. Sending it would turn a recoverable deny into a
// protocol error. This mirrors upstream's own `Default for ReviewDecision`, which is
// `Denied { rejection: "denied" }`.
//
// Note also that v1 casing (snake_case) must not be confused with v2's approvals, whose
// vocabulary is accept/decline/cancel. `abort` in particular must never be used here: it
// stops the session rather than continuing it.

// ReviewDecision is the v1 approval decision. Only the denial branch is modelled, because it
// is the only decision the SDK ever sends.
type ReviewDecision struct {
	Denied *ReviewDenial `json:"denied,omitempty"`
}

// ReviewDenial is the payload of the `denied` variant.
type ReviewDenial struct {
	Rejection string `json:"rejection"`
}

// DenyReview is the answer the SDK gives when no handler is configured. Its semantics
// upstream are "do not execute this, but continue the session and try something else".
func DenyReview() ReviewDecision {
	return ReviewDecision{Denied: &ReviewDenial{Rejection: "denied"}}
}

// ApplyPatchApprovalResponse is the reply to the legacy `applyPatchApproval` request.
type ApplyPatchApprovalResponse struct {
	Decision ReviewDecision `json:"decision"`
}

// ExecCommandApprovalResponse is the reply to the legacy `execCommandApproval` request.
type ExecCommandApprovalResponse struct {
	Decision ReviewDecision `json:"decision"`
}

// MethodNotFoundError marks an inbound server request whose method the SDK does not
// implement.
//
// It exists so the request loop can answer JSON-RPC -32601 "method not found" instead of
// -32603. The distinction matters to the server: -32603 means "I tried and failed" (which may
// end the turn), while -32601 means "I do not know this method", which it can handle.
type MethodNotFoundError struct {
	Method string
}

func (e *MethodNotFoundError) Error() string {
	return "protocol: method not found: " + e.Method
}

// Unwrap lets errors.Is(err, ErrUnsupportedServerRequest) keep working for callers that
// already test for the sentinel.
func (e *MethodNotFoundError) Unwrap() error { return ErrUnsupportedServerRequest }

type AttestationGenerateResponse struct {
	Token string `json:"token"`
}

type CurrentTimeReadResponse struct {
	CurrentTimeAt int64 `json:"currentTimeAt"`
}
