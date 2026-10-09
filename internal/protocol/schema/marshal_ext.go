// marshal_ext.go holds custom MarshalJSON implementations that extend the
// generated schema types. This file is NOT regenerated; it survives schema
// updates intact.
package schema

import (
	"encoding/json"
)

// MarshalJSON omits the Ephemeral field when false so the wire format stays
// compact. The alias breaks the recursion that would otherwise occur when
// calling json.Marshal on the same type.
func (r ThreadStartParams) MarshalJSON() ([]byte, error) {
	type alias struct {
		Model                 string             `json:"model,omitempty"`
		CWD                   string             `json:"cwd,omitempty"`
		ApprovalPolicy        *AskForApproval    `json:"approvalPolicy,omitempty"`
		ApprovalsReviewer     *ApprovalsReviewer `json:"approvalsReviewer,omitempty"`
		RuntimeWorkspaceRoots []string           `json:"runtimeWorkspaceRoots,omitempty"`
		Personality           string             `json:"personality,omitempty"`
		DynamicTools          []string           `json:"dynamicTools,omitempty"`
		Metadata              json.RawMessage    `json:"metadata,omitempty"`
		ModelProvider         string             `json:"modelProvider,omitempty"`
		Config                map[string]any     `json:"config,omitempty"`
		BaseInstructions      string             `json:"baseInstructions,omitempty"`
		DeveloperInstructions string             `json:"developerInstructions,omitempty"`
		ServiceTier           string             `json:"serviceTier,omitempty"`
		Sandbox               *SandboxMode       `json:"sandbox,omitempty"`
	}
	return json.Marshal(alias{
		Model:                 r.Model,
		CWD:                   r.CWD,
		ApprovalPolicy:        r.ApprovalPolicy,
		ApprovalsReviewer:     r.ApprovalsReviewer,
		RuntimeWorkspaceRoots: r.RuntimeWorkspaceRoots,
		Personality:           r.Personality,
		DynamicTools:          r.DynamicTools,
		Metadata:              r.Metadata,
		ModelProvider:         r.ModelProvider,
		Config:                r.Config,
		BaseInstructions:      r.BaseInstructions,
		DeveloperInstructions: r.DeveloperInstructions,
		ServiceTier:           r.ServiceTier,
		Sandbox:               r.Sandbox,
	})
}
