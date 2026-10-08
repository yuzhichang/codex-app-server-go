// Code generated from the vendored codex app-server stable schema. DO NOT EDIT.
//
// Current source: internal/protocol/schema/codex_app_server_protocol.v2.schemas.json
// (vendored by `make sync` from the pinned codex commit; see version.go).
//
// NOTE: the generated method constants below are NOT a coverage signal. A constant merely
// means the protocol declares the method; it says nothing about whether the SDK actually
// wires it up. Implementation coverage is tracked separately in
// gen/implemented-methods.json and enforced by `make conformance`
// (see tasks/plan-schema-alignment-and-coverage.md, T1.1).
// Custom extensions (MarshalJSON, DecodeParams, etc.) live in separate *_ext.go files.
package schema

import (
	"bytes"
	"encoding/json"
	"time"
)

// --- Enum types ---

type ThreadStatus string

const (
	ThreadStatusNotLoaded   ThreadStatus = "notLoaded"
	ThreadStatusIdle        ThreadStatus = "idle"
	ThreadStatusSystemError ThreadStatus = "systemError"
	ThreadStatusActive      ThreadStatus = "active"
)

type ThreadActiveFlag string

const (
	ThreadActiveFlagWaitingOnApproval  ThreadActiveFlag = "waitingOnApproval"
	ThreadActiveFlagWaitingOnUserInput ThreadActiveFlag = "waitingOnUserInput"
)

type TurnStatus string

const (
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusInterrupted TurnStatus = "interrupted"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInProgress  TurnStatus = "inProgress"
)

type TurnItemsView string

const (
	TurnItemsViewNotLoaded TurnItemsView = "notLoaded"
	TurnItemsViewSummary   TurnItemsView = "summary"
	TurnItemsViewFull      TurnItemsView = "full"
)

type ItemKind string

const (
	ItemKindUserMessage         ItemKind = "userMessage"
	ItemKindHookPrompt          ItemKind = "hookPrompt"
	ItemKindAgentMessage        ItemKind = "agentMessage"
	ItemKindPlan                ItemKind = "plan"
	ItemKindReasoning           ItemKind = "reasoning"
	ItemKindCommandExecution    ItemKind = "commandExecution"
	ItemKindFileChange          ItemKind = "fileChange"
	ItemKindMCPToolCall         ItemKind = "mcpToolCall"
	ItemKindDynamicToolCall     ItemKind = "dynamicToolCall"
	ItemKindCollabAgentToolCall ItemKind = "collabAgentToolCall"
	ItemKindSubAgentActivity    ItemKind = "subAgentActivity"
	ItemKindWebSearch           ItemKind = "webSearch"
	ItemKindImageView           ItemKind = "imageView"
	ItemKindSleep               ItemKind = "sleep"
	ItemKindImageGeneration     ItemKind = "imageGeneration"
	ItemKindEnteredReviewMode   ItemKind = "enteredReviewMode"
	ItemKindExitedReviewMode    ItemKind = "exitedReviewMode"
	ItemKindContextCompaction   ItemKind = "contextCompaction"
	// Present upstream as a ThreadItem tag but missing here, so items of this kind were
	// unrecognised. Checked against the aggregate's ThreadItem discriminator values.
	ItemKindFunctionCallOutput ItemKind = "functionCallOutput"
)

type CommandExecutionApprovalDecision string

const (
	CommandExecutionApprovalDecisionAccept                        CommandExecutionApprovalDecision = "accept"
	CommandExecutionApprovalDecisionAcceptForSession              CommandExecutionApprovalDecision = "acceptForSession"
	CommandExecutionApprovalDecisionAcceptWithExecpolicyAmendment CommandExecutionApprovalDecision = "acceptWithExecpolicyAmendment"
	CommandExecutionApprovalDecisionApplyNetworkPolicyAmendment   CommandExecutionApprovalDecision = "applyNetworkPolicyAmendment"
	CommandExecutionApprovalDecisionDecline                       CommandExecutionApprovalDecision = "decline"
	CommandExecutionApprovalDecisionCancel                        CommandExecutionApprovalDecision = "cancel"
)

type FileChangeApprovalDecision string

const (
	FileChangeApprovalDecisionAccept           FileChangeApprovalDecision = "accept"
	FileChangeApprovalDecisionAcceptForSession FileChangeApprovalDecision = "acceptForSession"
	FileChangeApprovalDecisionDecline          FileChangeApprovalDecision = "decline"
	FileChangeApprovalDecisionCancel           FileChangeApprovalDecision = "cancel"
)

type PermissionGrantScope string

const (
	PermissionGrantScopeSession PermissionGrantScope = "session"
	PermissionGrantScopeTurn    PermissionGrantScope = "turn"
)

// --- Shared data types ---

// GitInfo is the Git metadata stored on a thread.
//
// Upstream is {sha?, branch?, originUrl?}. This previously declared Root/Commit/Remote/
// Dirty/Detached instead -- five invented fields, only `branch` matching -- so every real
// value was dropped on decode and the read shape disagreed with the patch shape used by
// thread/metadata/update.
//
// It is not generated because nothing reachable from a method's params/response references
// it: its only parent is Thread, which is itself hand-written.
//
// Optional strings use plain `string` with omitempty, matching the generator's convention
// for every other optional string in this package. That cannot distinguish "absent" from
// "present but empty"; ThreadMetadataGitInfo uses pointers precisely because the *patch*
// direction needs to tell those apart (nil leaves the field unchanged, "" clears it).
type GitInfo struct {
	Branch    string `json:"branch,omitempty"`
	OriginURL string `json:"originUrl,omitempty"`
	Sha       string `json:"sha,omitempty"`
}

// --- Skill metadata (hand-written: unreachable from any method's params/response) ---
//
// gen_go_types --from-surface only emits types reachable from a method's params or response.
// These two are referenced only by other hand-written skill types, so the generator never
// reaches them, and they must be maintained by hand. Their shapes are the generator's own
// output, so type-shape-check compares clean.

// SkillInterface mirrors the upstream `SkillInterface` definition.
type SkillInterface struct {
	BrandColor       string           `json:"brandColor,omitempty"`
	DefaultPrompt    string           `json:"defaultPrompt,omitempty"`
	DisplayName      string           `json:"displayName,omitempty"`
	IconLarge        *AbsolutePathBuf `json:"iconLarge,omitempty"`
	IconLargeURL     string           `json:"iconLargeUrl,omitempty"`
	IconSmall        *AbsolutePathBuf `json:"iconSmall,omitempty"`
	IconSmallURL     string           `json:"iconSmallUrl,omitempty"`
	ShortDescription string           `json:"shortDescription,omitempty"`
}

// SkillMetadata mirrors the upstream `SkillMetadata` definition.
type SkillMetadata struct {
	Dependencies     *SkillDependencies  `json:"dependencies,omitempty"`
	Description      string              `json:"description"`
	Enabled          bool                `json:"enabled"`
	Interface        *SkillInterface     `json:"interface,omitempty"`
	Name             string              `json:"name"`
	Path             LegacyAppPathString `json:"path"`
	PluginID         string              `json:"pluginId,omitempty"`
	Scope            SkillScope          `json:"scope"`
	ShortDescription string              `json:"shortDescription,omitempty"`
}

// --- Core data models (schema-faithful; runtime types with custom decoders live in protocol) ---

// ThreadItem mirrors the Item definition from the schema.
// For the full runtime type with payload encoding, see protocol.Item.
type ThreadItem struct {
	ID   string   `json:"id,omitempty"`
	Type ItemKind `json:"type"`
}

// Turn mirrors the upstream Turn definition.
// For the full runtime type with flexible time parsing, see protocol.Turn.
//
// There is deliberately no Usage field: upstream Turn carries none, and turn/completed
// sends only {threadId, turn}. Usage arrives on thread/tokenUsage/updated as
// ThreadTokenUsage.
// Thread mirrors the Thread definition from the schema.
// For the full runtime type with flexible status decoding, see protocol.Thread.
type Thread struct {
	ID             string       `json:"id,omitempty"`
	SessionID      string       `json:"sessionId,omitempty"`
	ForkedFromID   string       `json:"forkedFromId,omitempty"`
	ParentThreadID string       `json:"parentThreadId,omitempty"`
	Preview        string       `json:"preview,omitempty"`
	Ephemeral      bool         `json:"ephemeral,omitempty"`
	ModelProvider  string       `json:"modelProvider,omitempty"`
	CreatedAt      *time.Time   `json:"createdAt,omitempty"`
	UpdatedAt      *time.Time   `json:"updatedAt,omitempty"`
	Status         ThreadStatus `json:"status,omitempty"`
	Path           string       `json:"path,omitempty"`
	CWD            string       `json:"cwd,omitempty"`
	CliVersion     string       `json:"cliVersion,omitempty"`
	Source         string       `json:"source,omitempty"`
	ThreadSource   string       `json:"threadSource,omitempty"`
	AgentNickname  string       `json:"agentNickname,omitempty"`
	AgentRole      string       `json:"agentRole,omitempty"`
	GitInfo        *GitInfo     `json:"gitInfo,omitempty"`
	Name           string       `json:"name,omitempty"`
	Turns          []Turn       `json:"turns,omitempty"`
}

// NOTE: the SDK deliberately declares NO JSON-RPC envelope types here.
//
// Upstream defines none either -- `app-server-protocol/src/rpc.rs` is explicit that it
// does not do true JSON-RPC 2.0 -- and the wire envelopes are owned by
// internal/transport (requestEnvelope / notificationEnvelope / replyEnvelope). The
// former RPCRequest / RPCResponse / RPCNotification / RPCError / InitializedNotification
// types were SDK inventions with no upstream counterpart and have been removed.

// --- Initialize ---

type ClientInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// InitializeCapabilities mirrors the upstream InitializeCapabilities
// (app-server-protocol/src/protocol/v1.rs:46-70).
//
// Upstream `capabilities` is `Option<InitializeCapabilities>`; InitializeParams therefore
// holds a pointer so that "no capabilities" is actually omitted from the wire. A struct
// value would always be serialized (encoding/json never treats a struct as empty).
type InitializeCapabilities struct {
	// ExplicitGatewayOauth uses explicit gateway OAuth login instead of automatic browser
	// authorization. Applies to the app-server's gateway runtime.
	ExplicitGatewayOauth bool `json:"explicitGatewayOauth,omitempty"`

	// ExperimentalAPI opts into experimental methods and fields. The SDK defaults this to
	// FALSE: per decision R2 no experimental surface is implemented, so declaring it would
	// only invite notifications we deliberately do not handle.
	ExperimentalAPI bool `json:"experimentalApi,omitempty"`

	// RequestAttestation opts into `attestation/generate` server requests. Defaults to
	// false, matching decision R4 (no attestation handler is provided).
	RequestAttestation bool `json:"requestAttestation,omitempty"`

	// MCPServerOpenAIFormElicitation is the legacy opt-in for the `openai/form` MCP
	// extension. New clients should declare it via Extensions instead.
	McpServerOpenaiFormElicitation bool `json:"mcpServerOpenaiFormElicitation,omitempty"`

	// OptOutNotificationMethods lists exact notification method names to suppress for this
	// connection (for example "thread/started").
	//
	// D2: this was previously typed `bool`, which made the field silently useless --
	// upstream is `Option<Vec<String>>` (protocol/v1.rs:65).
	OptOutNotificationMethods []string `json:"optOutNotificationMethods,omitempty"`

	// Extensions declares MCP extension settings.
	Extensions map[string]any `json:"extensions,omitempty"`
}

type InitializeParams struct {
	ClientInfo   ClientInfo              `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities,omitempty"`
}

type InitializeResponse struct {
	UserAgent      string `json:"userAgent,omitempty"`
	CodexHome      string `json:"codexHome,omitempty"`
	PlatformFamily string `json:"platformFamily,omitempty"`
	PlatformOS     string `json:"platformOs,omitempty"`
}

// --- Thread/Turn RPC requests ---

type ThreadStartParams struct {
	Model                 string             `json:"model,omitempty"`
	CWD                   string             `json:"cwd,omitempty"`
	ApprovalPolicy        *AskForApproval    `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer     *ApprovalsReviewer `json:"approvalsReviewer,omitempty"`
	RuntimeWorkspaceRoots []string           `json:"runtimeWorkspaceRoots,omitempty"`
	Environments          []string           `json:"environments,omitempty"`
	Personality           string             `json:"personality,omitempty"`
	DynamicTools          []string           `json:"dynamicTools,omitempty"`
	Ephemeral             bool               `json:"ephemeral,omitempty"`
	Metadata              json.RawMessage    `json:"metadata,omitempty"`
}

// ThreadResumeParams resumes a thread.
//
// Aligned with the upstream field set. Four fields this used to carry did not exist upstream
// -- `history`, `path` and `initialTurnsPage` are invented, and `excludeTurns` was typed
// []string where upstream is a plain boolean. Nothing could set it correctly, and because the
// slice was omitempty it was never sent either.
//
// Types are deliberately the SDK's ergonomic ones rather than the generated shapes. Upstream
// declares approvalPolicy/approvalsReviewer/personality as unions or enums, and the generated
// Go types for them live in an internal package, so a field typed that way could not be set by
// an external caller at all. Unifying the SDK's ApprovalMode/SandboxMode with the schema enums
// is the follow-up that would let these fields adopt the generated shape.
type ThreadResumeParams struct {
	ThreadID string `json:"threadId"`

	// ApprovalPolicy overrides the approval policy.
	ApprovalPolicy *AskForApproval `json:"approvalPolicy,omitempty"`
	// ApprovalsReviewer routes approval requests to a reviewer.
	ApprovalsReviewer *ApprovalsReviewer `json:"approvalsReviewer,omitempty"`
	// BaseInstructions / DeveloperInstructions override the thread's instructions.
	BaseInstructions      string `json:"baseInstructions,omitempty"`
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
	// Config is an open-ended config overlay; upstream types it as an object or null.
	Config map[string]any `json:"config,omitempty"`
	// CWD sets the working directory for the resumed thread.
	CWD string `json:"cwd,omitempty"`
	// ExcludeTurns drops the stored turns instead of replaying them.
	ExcludeTurns bool `json:"excludeTurns,omitempty"`
	// Model / ModelProvider / ServiceTier override the thread's model settings.
	Model         string `json:"model,omitempty"`
	ModelProvider string `json:"modelProvider,omitempty"`
	ServiceTier   string `json:"serviceTier,omitempty"`
	// Personality accepts the upstream personality wire value.
	Personality string `json:"personality,omitempty"`
	// Sandbox overrides the sandbox mode for the resumed thread.
	Sandbox *SandboxMode `json:"sandbox,omitempty"`
}

type ThreadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

// AskForApproval is the approval policy for a turn or thread.
//
// Upstream declares it as a union of a plain enum string (untrusted / on-request / never) and
// an object {"granular": {...}}. Because one arm is a bare string, this cannot be a
// type-tagged struct the way SandboxPolicy is -- a struct always encodes as an object. It
// therefore carries its own JSON encoding. Prefer the constructors.
//
// This replaces the SDK's ApprovalMode, which mixed two upstream enums: it offered `deny_all`
// (valid in neither) and `auto_review` (an ApprovalsReviewer value), so a caller could ask for
// an approval policy the server does not define.
type AskForApproval struct {
	policy   string
	granular *AskForApprovalGranular
}

// AskForApproval policy values.
const (
	AskForApprovalUntrusted = "untrusted"
	AskForApprovalOnRequest = "on-request"
	AskForApprovalNever     = "never"
)

// ApprovalUntrusted asks for approval before running anything not explicitly trusted.
func ApprovalUntrusted() AskForApproval { return AskForApproval{policy: AskForApprovalUntrusted} }

// ApprovalOnRequest asks for approval when the agent decides it needs it.
func ApprovalOnRequest() AskForApproval { return AskForApproval{policy: AskForApprovalOnRequest} }

// ApprovalNever never asks for approval.
func ApprovalNever() AskForApproval { return AskForApproval{policy: AskForApprovalNever} }

// ApprovalGranular asks for approval per category.
func ApprovalGranular(g AskForApprovalGranular) AskForApproval {
	return AskForApproval{granular: &g}
}

// Policy returns the enum arm, or "" when the granular arm is set.
func (a AskForApproval) Policy() string { return a.policy }

// Granular returns the granular arm, or nil for an enum policy.
func (a AskForApproval) Granular() *AskForApprovalGranular { return a.granular }

// MarshalJSON writes the string arm bare and the granular arm as an object, matching upstream.
func (a AskForApproval) MarshalJSON() ([]byte, error) {
	if a.granular != nil {
		return json.Marshal(struct {
			Granular *AskForApprovalGranular `json:"granular"`
		}{a.granular})
	}
	return json.Marshal(a.policy)
}

// UnmarshalJSON accepts either arm.
func (a *AskForApproval) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		var obj struct {
			Granular *AskForApprovalGranular `json:"granular"`
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		a.granular, a.policy = obj.Granular, ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	a.policy, a.granular = s, nil
	return nil
}

// AskForApprovalGranular selects which approval categories apply.
//
// Field names are snake_case upstream, unlike the rest of the protocol, so the JSON tags are
// deliberately not camelCase. The three required fields have no omitempty: upstream rejects a
// granular object that omits them.
type AskForApprovalGranular struct {
	MCPElicitations    bool `json:"mcp_elicitations"`
	Rules              bool `json:"rules"`
	SandboxApproval    bool `json:"sandbox_approval"`
	RequestPermissions bool `json:"request_permissions,omitempty"`
	SkillApproval      bool `json:"skill_approval,omitempty"`
}

// SandboxPolicy selects the sandbox for a turn or thread.
//
// Upstream declares it as a `type`-tagged union of four OBJECT variants
// (dangerFullAccess, readOnly, externalSandbox, workspaceWrite). It is modelled here as one
// flat struct with an explicit Type discriminator plus the union of every variant's fields,
// consistent with UserInput and ReviewTarget. Prefer the constructors.
//
// This replaces a `string` field. The wire shape is an object and the discriminators are
// camelCase (readOnly/workspaceWrite/dangerFullAccess) -- NOT the SandboxMode spellings
// (read-only/workspace-write/danger-full-access). The old field sent a bare mode string, so
// it was rejected twice over: wrong JSON type and wrong value.
type SandboxPolicy struct {
	Type string `json:"type"`

	// NetworkAccess is a union upstream (boolean, or an object describing proxy/allow rules),
	// so it is passed through. The common case is to omit it.
	NetworkAccess json.RawMessage `json:"networkAccess,omitempty"`

	// workspaceWrite only.
	ExcludeSlashTmp     bool     `json:"excludeSlashTmp,omitempty"`
	ExcludeTmpdirEnvVar bool     `json:"excludeTmpdirEnvVar,omitempty"`
	WritableRoots       []string `json:"writableRoots,omitempty"`
}

// SandboxPolicyType values, matching the upstream discriminator strings.
const (
	SandboxPolicyTypeDangerFullAccess = "dangerFullAccess"
	SandboxPolicyTypeReadOnly         = "readOnly"
	SandboxPolicyTypeExternalSandbox  = "externalSandbox"
	SandboxPolicyTypeWorkspaceWrite   = "workspaceWrite"
)

// DangerFullAccessPolicy runs without sandboxing.
func DangerFullAccessPolicy() SandboxPolicy {
	return SandboxPolicy{Type: SandboxPolicyTypeDangerFullAccess}
}

// ReadOnlyPolicy allows reads only.
func ReadOnlyPolicy() SandboxPolicy {
	return SandboxPolicy{Type: SandboxPolicyTypeReadOnly}
}

// ExternalSandboxPolicy uses an externally managed sandbox.
func ExternalSandboxPolicy() SandboxPolicy {
	return SandboxPolicy{Type: SandboxPolicyTypeExternalSandbox}
}

// WorkspaceWritePolicy writes only inside the workspace. Set WritableRoots and the Exclude*
// fields on the returned value to widen or narrow it.
func WorkspaceWritePolicy() SandboxPolicy {
	return SandboxPolicy{Type: SandboxPolicyTypeWorkspaceWrite}
}

// SandboxPolicyFromMode maps a SandboxMode onto the equivalent policy object.
//
// The two are different types upstream with different value spellings, so this conversion is
// not cosmetic: it is what stops a mode from being sent where a policy object is required.
func SandboxPolicyFromMode(m SandboxMode) *SandboxPolicy {
	policy := DangerFullAccessPolicy()
	switch m {
	case SandboxModeReadOnly:
		policy = ReadOnlyPolicy()
	case SandboxModeWorkspaceWrite:
		policy = WorkspaceWritePolicy()
	}
	return &policy
}

type TurnStartParams struct {
	ThreadID            string `json:"threadId"`
	Input               string `json:"input,omitempty"`
	ClientUserMessageID string `json:"clientUserMessageId,omitempty"`
	CWD                 string `json:"cwd,omitempty"`
	// ApprovalPolicy overrides the approval policy for this turn and onwards.
	ApprovalPolicy *AskForApproval `json:"approvalPolicy,omitempty"`
	// ApprovalsReviewer routes approval requests to a reviewer. This is where `auto_review`
	// belongs -- it is an ApprovalsReviewer value, not an approval policy.
	ApprovalsReviewer *ApprovalsReviewer `json:"approvalsReviewer,omitempty"`
	// SandboxPolicy must be a tagged OBJECT upstream, not a mode string: sending
	// "workspace-write" is rejected, and the discriminators are the camelCase
	// readOnly/workspaceWrite/dangerFullAccess -- not the SandboxMode spellings.
	// Use the SandboxPolicy constructors.
	SandboxPolicy     *SandboxPolicy  `json:"sandboxPolicy,omitempty"`
	Permissions       []string        `json:"permissions,omitempty"`
	Model             string          `json:"model,omitempty"`
	ServiceTier       string          `json:"serviceTier,omitempty"`
	Effort            string          `json:"effort,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	OutputSchema      json.RawMessage `json:"outputSchema,omitempty"`
	CollaborationMode string          `json:"collaborationMode,omitempty"`
	MultiAgentMode    string          `json:"multiAgentMode,omitempty"`
	Environments      []string        `json:"environments,omitempty"`
}

type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// --- Thread fork / list / archive / setName / rollback ---

type ThreadListResponse struct {
	Threads []Thread `json:"threads"`
	Cursor  string   `json:"cursor,omitempty"`
}

type ThreadArchiveParams struct {
	ThreadID string `json:"threadId"`
}

type ThreadUnarchiveParams struct {
	ThreadID string `json:"threadId"`
}

type ThreadSetNameParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

// ThreadRevertParams excludes beforeTurnID, together with every later turn, from the
// thread's replacement history.
type ThreadRevertParams struct {
	ThreadID     string `json:"threadId"`
	BeforeTurnID string `json:"beforeTurnId"`
}

// ThreadRevertResponse returns the updated thread metadata plus cursors for hydrating the
// retained history. `Thread.Turns` is always empty here: page it back with
// thread/turns/list (via TurnsBackwardsCursor) and thread/items/list.
type ThreadRevertResponse struct {
	Thread               Thread  `json:"thread"`
	TurnsBackwardsCursor *string `json:"turnsBackwardsCursor,omitempty"`
	ItemsBackwardsCursor *string `json:"itemsBackwardsCursor,omitempty"`
}

// --- Turn steer ---

// TurnSteerParams adds input to a turn that is already running.
//
// ExpectedTurnID is REQUIRED upstream. The field used to be named TurnID and was sent as
// "turnId", which upstream does not define on this request -- so the required expectedTurnId
// was omitted entirely and every steer was rejected. It had no omitempty, so the wrong field
// was always sent.
type TurnSteerParams struct {
	ThreadID string `json:"threadId"`
	// ExpectedTurnID is the turn this input is steered into. Required.
	ExpectedTurnID      string `json:"expectedTurnId"`
	Input               string `json:"input"`
	ClientUserMessageID string `json:"clientUserMessageId,omitempty"`
}

// --- Review ---

// ReviewStartParams starts a review of a thread.
//
// `target` is REQUIRED upstream and says what to review. It was previously absent, so the
// SDK sent {threadId, turnId} and the server rejected every review/start; `turnId` was an
// invented field with no upstream counterpart and is gone with it.
type ReviewStartParams struct {
	ThreadID string `json:"threadId"`
	// Target is required. Use one of the ReviewTarget constructors below.
	Target ReviewTarget `json:"target"`
	// Delivery defaults to inline upstream.
	Delivery *ReviewDelivery `json:"delivery,omitempty"`
}

// ReviewDelivery is how a review is delivered.
type ReviewDelivery string

const (
	// ReviewDeliveryInline runs the review inside the current thread.
	ReviewDeliveryInline ReviewDelivery = "inline"
	// ReviewDeliveryDetached runs it in its own thread (see ReviewStartResponse.ReviewThreadID).
	ReviewDeliveryDetached ReviewDelivery = "detached"
)

// ReviewTarget selects what a review covers.
//
// Upstream declares a `type`-tagged union with four variants (uncommittedChanges, baseBranch,
// commit, custom). It is modelled here as one flat struct with an explicit Type discriminator
// plus the union of every variant's fields -- the same approach used for UserInput and the
// elicitation payloads, and for the same reason: no field is dropped and no future variant is
// rejected outright. Prefer the constructors.
type ReviewTarget struct {
	Type string `json:"type"`

	// baseBranch
	Branch string `json:"branch,omitempty"`
	// commit
	Sha   string  `json:"sha,omitempty"`
	Title *string `json:"title,omitempty"`
	// custom
	Instructions string `json:"instructions,omitempty"`
}

// ReviewTargetType values, matching the upstream discriminator strings.
const (
	ReviewTargetTypeUncommittedChanges = "uncommittedChanges"
	ReviewTargetTypeBaseBranch         = "baseBranch"
	ReviewTargetTypeCommit             = "commit"
	ReviewTargetTypeCustom             = "custom"
)

// UncommittedChangesTarget reviews the working tree: staged, unstaged and untracked files.
func UncommittedChangesTarget() ReviewTarget {
	return ReviewTarget{Type: ReviewTargetTypeUncommittedChanges}
}

// BaseBranchTarget reviews everything since the given base branch.
func BaseBranchTarget(branch string) ReviewTarget {
	return ReviewTarget{Type: ReviewTargetTypeBaseBranch, Branch: branch}
}

// CommitTarget reviews a single commit. title is optional context for the UI.
func CommitTarget(sha string, title *string) ReviewTarget {
	return ReviewTarget{Type: ReviewTargetTypeCommit, Sha: sha, Title: title}
}

// CustomTarget reviews according to free-form instructions.
func CustomTarget(instructions string) ReviewTarget {
	return ReviewTarget{Type: ReviewTargetTypeCustom, Instructions: instructions}
}

// --- Turn diff ---
//
// NOTE: there is no `turn/diff` request upstream. The turn's aggregated diff is only ever
// PUSHED, via the `turn/diff/updated` notification (TurnDiffUpdatedEvent). Per-file diffs
// are pullable from ThreadItem fileChange entries. The old pull-style TurnDiffRequest /
// TurnDiffResult pair was removed accordingly (see plan T1.5).

// --- Skills ---

// SkillScope enumerates where a skill is sourced from.
type SkillScope string

const (
	SkillScopeUser   SkillScope = "user"
	SkillScopeRepo   SkillScope = "repo"
	SkillScopeSystem SkillScope = "system"
	SkillScopeAdmin  SkillScope = "admin"
)

// SkillErrorInfo describes a parse/load error for a skill file.
type SkillErrorInfo struct {
	Message string `json:"message"`
	Path    string `json:"path"`
}

// SkillInterface holds UI-facing metadata for a skill (display name, icon, etc.).
// SkillToolDependency describes a tool dependency declared by a skill.
type SkillToolDependency struct {
	Type        string `json:"type"`
	Value       string `json:"value"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	Transport   string `json:"transport,omitempty"`
	URL         string `json:"url,omitempty"`
}

// SkillDependencies lists tool dependencies for a skill.
type SkillDependencies struct {
	Tools []SkillToolDependency `json:"tools"`
}

// SkillMetadata is the full metadata for a skill (name, path, scope, etc.).
// SkillSummary is a lighter view of a skill (no path or scope required).
type SkillSummary struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Enabled          bool            `json:"enabled"`
	Path             string          `json:"path,omitempty"`
	ShortDescription string          `json:"shortDescription,omitempty"`
	Interface        *SkillInterface `json:"interface,omitempty"`
}

// SkillsListEntry groups skills and errors for a single cwd.
type SkillsListEntry struct {
	CWD    string           `json:"cwd"`
	Skills []SkillMetadata  `json:"skills"`
	Errors []SkillErrorInfo `json:"errors"`
}

// SkillsListParams is the request type for the skills/list RPC.
type SkillsListParams struct {
	Cwds        []string `json:"cwds,omitempty"`
	ForceReload bool     `json:"forceReload,omitempty"`
}

// SkillsListResponse is the response type for the skills/list RPC.
type SkillsListResponse struct {
	Data []SkillsListEntry `json:"data"`
}

// SkillsChangedNotification is sent when watched skill files change on disk.
type SkillsChangedNotification struct{}

// SkillsConfigWriteParams enables or disables a skill by name or path.
type SkillsConfigWriteParams struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name,omitempty"`
	Path    string `json:"path,omitempty"`
}

// SkillsConfigWriteResponse reports the effective enabled state after a config write.
type SkillsConfigWriteResponse struct {
	EffectiveEnabled bool `json:"effectiveEnabled"`
}

// SkillsExtraRootsSetParams sets extra filesystem roots to scan for skills.
type SkillsExtraRootsSetParams struct {
	ExtraRoots []string `json:"extraRoots"`
}

// SkillsExtraRootsSetResponse is the empty response for skills/extraRoots/set.
type SkillsExtraRootsSetResponse struct{}

// PluginSkillReadParams reads the raw content of a remote plugin skill.
type PluginSkillReadParams struct {
	RemoteMarketplaceName string `json:"remoteMarketplaceName"`
	RemotePluginID        string `json:"remotePluginId"`
	SkillName             string `json:"skillName"`
}

// PluginSkillReadResponse returns the raw content of a remote plugin skill.
type PluginSkillReadResponse struct {
	Contents string `json:"contents,omitempty"`
}

// --- ThreadGoal ---

// ThreadGoalStatus enumerates the lifecycle states of a thread goal.
type ThreadGoalStatus string

const (
	ThreadGoalStatusActive        ThreadGoalStatus = "active"
	ThreadGoalStatusPaused        ThreadGoalStatus = "paused"
	ThreadGoalStatusBlocked       ThreadGoalStatus = "blocked"
	ThreadGoalStatusUsageLimited  ThreadGoalStatus = "usageLimited"
	ThreadGoalStatusBudgetLimited ThreadGoalStatus = "budgetLimited"
	ThreadGoalStatusComplete      ThreadGoalStatus = "complete"
)

// ThreadGoal tracks a goal/objective set for a thread, including usage budgets.
type ThreadGoal struct {
	ThreadID        string           `json:"threadId"`
	Objective       string           `json:"objective"`
	Status          ThreadGoalStatus `json:"status"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
	TimeUsedSeconds int64            `json:"timeUsedSeconds"`
	TokensUsed      int64            `json:"tokensUsed"`
	TokenBudget     *int64           `json:"tokenBudget,omitempty"`
}

// ThreadGoalSetParams is the request type for threadGoal/set.
// ThreadGoalSetResponse is the response type for threadGoal/set.
type ThreadGoalSetResponse struct {
	Goal ThreadGoal `json:"goal"`
}

// ThreadGoalGetParams is the request type for threadGoal/get.
type ThreadGoalGetParams struct {
	ThreadID string `json:"threadId"`
}

// ThreadGoalGetResponse is the response type for threadGoal/get.
type ThreadGoalGetResponse struct {
	Goal *ThreadGoal `json:"goal,omitempty"`
}

// ThreadGoalClearParams is the request type for threadGoal/clear.
// ThreadGoalClearResponse is the response type for threadGoal/clear.
type ThreadGoalClearResponse struct {
	Cleared bool `json:"cleared"`
}

// ThreadGoalUpdatedNotification is emitted when a thread's goal is created or updated.
type ThreadGoalUpdatedNotification struct {
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId,omitempty"`
	Goal     ThreadGoal `json:"goal"`
}

// ThreadGoalClearedNotification is emitted when a thread's goal is cleared.
type ThreadGoalClearedNotification struct {
	ThreadID string `json:"threadId"`
}
