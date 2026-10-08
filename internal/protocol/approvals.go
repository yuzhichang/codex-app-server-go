package protocol

import (
	"encoding/json"

	schematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"
)

// Approval decision enum aliases -- canonical definitions live in the generated schema package.

type ApprovalDecision = schematypes.ApprovalDecision
type FileChangeApprovalDecision = schematypes.FileChangeApprovalDecision
type PermissionsScope = schematypes.PermissionsScope

const (
	ApprovalDecisionAccept                        = schematypes.ApprovalDecisionAccept
	ApprovalDecisionAcceptForSession              = schematypes.ApprovalDecisionAcceptForSession
	ApprovalDecisionAcceptWithExecpolicyAmendment = schematypes.ApprovalDecisionAcceptWithExecpolicyAmendment
	ApprovalDecisionApplyNetworkPolicyAmendment   = schematypes.ApprovalDecisionApplyNetworkPolicyAmendment
	ApprovalDecisionDecline                       = schematypes.ApprovalDecisionDecline
	ApprovalDecisionCancel                        = schematypes.ApprovalDecisionCancel
)

const (
	FileChangeApprovalDecisionAccept           = schematypes.FileChangeApprovalDecisionAccept
	FileChangeApprovalDecisionAcceptForSession = schematypes.FileChangeApprovalDecisionAcceptForSession
	FileChangeApprovalDecisionDecline          = schematypes.FileChangeApprovalDecisionDecline
	FileChangeApprovalDecisionCancel           = schematypes.FileChangeApprovalDecisionCancel
)

const (
	PermissionsScopeSession = schematypes.PermissionsScopeSession
	PermissionsScopeTurn    = schematypes.PermissionsScopeTurn
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
	Decision ApprovalDecision `json:"decision"`
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

type PermissionsApprovalRequest struct {
	ItemID      string           `json:"itemId,omitempty"`
	ThreadID    string           `json:"threadId,omitempty"`
	TurnID      string           `json:"turnId,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	Permissions []string         `json:"permissions,omitempty"`
	Scope       PermissionsScope `json:"scope,omitempty"`
}

type PermissionsApprovalResponse struct {
	Permissions []string         `json:"permissions,omitempty"`
	Scope       PermissionsScope `json:"scope,omitempty"`
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

type UserInputResponse struct {
	Answers map[string]string `json:"answers,omitempty"`
}

type UserInputResult = UserInputResponse

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

type AttestationGenerateResponse struct {
	Token string `json:"token"`
}

type CurrentTimeReadResponse struct {
	CurrentTimeAt int64 `json:"currentTimeAt"`
}
