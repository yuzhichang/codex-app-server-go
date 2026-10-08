package codexgo

import (
	"encoding/json"
	"strings"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

// TurnResult holds the outcome of a completed turn.
type TurnResult struct {
	// Turn is the final Turn data as returned by the server.
	Turn Turn
	// Items accumulates all items observed during the turn via streaming events.
	Items []Item
	// Usage is the latest per-thread token-usage snapshot observed during the turn.
	//
	// It comes from the `thread/tokenUsage/updated` notification: upstream carries usage
	// nowhere else (turn/completed sends only {threadId, turn}), so this is nil unless that
	// notification arrived.
	Usage *ThreadTokenUsage
	// Error is the structured error from the TurnCompleted event, if any.
	Error *TurnError
	// DeltaText holds agent-message text reconstructed from streaming delta events.
	// It is only set when Items carry no extractable agent text.
	DeltaText string
}

// FinalAgentText returns the text of the last agent-message item in the result.
// It searches Items, Turn.Items, and finally DeltaText (reconstructed from
// streaming delta events). Returns "" if no agent message is found.
func (r *TurnResult) FinalAgentText() string {
	if r == nil {
		return ""
	}
	lists := [][]Item{r.Items, r.Turn.Items}
	for _, items := range lists {
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Kind != protocol.ItemKindAgentMessage {
				continue
			}
			if text := extractTextCandidate(items[i].PayloadBytes()); text != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	if r.DeltaText != "" {
		return strings.TrimSpace(r.DeltaText)
	}
	return ""
}

// TurnOption is a functional option that configures a TurnStartParams.
type TurnOption func(*TurnStartParams)

// WithModel sets the model for the turn.
func WithModel(model string) TurnOption {
	return func(r *TurnStartParams) {
		r.Model = model
	}
}

// WithApprovalPolicy sets the approval policy for the turn.
//
// This replaces WithApprovalPolicy(string) and WithApprovalMode(ApprovalMode): a bare string
// could carry any value at all, and ApprovalMode offered two the server does not define
// (deny_all, and auto_review which belongs to ApprovalsReviewer). Use the Approval* builders.
func WithApprovalPolicy(policy AskForApproval) TurnOption {
	return func(r *TurnStartParams) {
		p := policy
		r.ApprovalPolicy = &p
	}
}

// WithApprovalsReviewer routes approval requests to a reviewer.
func WithApprovalsReviewer(reviewer ApprovalsReviewer) TurnOption {
	return func(r *TurnStartParams) {
		v := reviewer
		r.ApprovalsReviewer = &v
	}
}

// WithSandboxPolicy sets the sandbox policy for the turn.
//
// This replaces WithSandbox(string). That took a bare mode string and assigned it straight to
// the wire field, which upstream types as a tagged OBJECT -- so the payload was rejected on
// two counts, wrong JSON type and the wrong value spelling. A string cannot be a valid policy,
// which is why the string-taking option is gone rather than reinterpreted.
func WithSandboxPolicy(policy SandboxPolicy) TurnOption {
	return func(r *TurnStartParams) {
		p := policy
		r.SandboxPolicy = &p
	}
}

// WithSandboxMode sets the sandbox policy from a SandboxMode.
//
// The mode and the policy are different upstream types with different value spellings, so this
// converts rather than casts. Assigning the mode string directly is the bug it replaces.
func WithSandboxMode(mode SandboxMode) TurnOption {
	return func(r *TurnStartParams) {
		r.SandboxPolicy = SandboxPolicyFromMode(mode)
	}
}

// WithInputs sets typed multi-part inputs for the turn, replacing any plain
// string input. Use TextInput, ImageInput, LocalImageInput, SkillInput, and
// MentionInput to construct the items.
func WithInputs(inputs ...TurnInput) TurnOption {
	return func(r *TurnStartParams) {
		r.Input = encodeInputs(inputs)
	}
}

// WithCWD sets the working directory for the turn.
func WithCWD(cwd string) TurnOption {
	return func(r *TurnStartParams) {
		r.CWD = cwd
	}
}

// WithEffort sets the effort level for the turn.
func WithEffort(effort string) TurnOption {
	return func(r *TurnStartParams) {
		r.Effort = effort
	}
}

// NOTE: WithSkill was removed. `TurnStartParams` has no `skill` field upstream, so the
// option set a field that could never be transmitted -- it had no effect on the server. The
// hand-written marshaller in internal/protocol/schema/marshal_ext.go re-lists every field by
// hand, and `Skill` was omitted from it, which is why the option appeared to work while
// doing nothing.

// WithOutputSchema sets the output schema for constraining the assistant's final message.
func WithOutputSchema(schema any) TurnOption {
	return func(r *TurnStartParams) {
		data, _ := json.Marshal(schema)
		r.OutputSchema = data
	}
}

// applyTurnOptions applies all TurnOption values to req.
func applyTurnOptions(req *TurnStartParams, opts []TurnOption) {
	for _, o := range opts {
		o(req)
	}
}

// threadCompactRequest is the payload for the thread/compact RPC call.
type threadCompactRequest struct {
	ThreadID string `json:"threadId"`
}

// Ensure protocol constants are used (avoids import cycle if they're ever split).
var _ = protocol.MethodThreadCompact
