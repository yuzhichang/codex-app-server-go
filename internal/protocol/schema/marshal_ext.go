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
	})
}
