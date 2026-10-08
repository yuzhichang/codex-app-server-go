package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestDecodeCommandExecutionApprovalRequest verifies that the command execution
// approval method is decoded into the correct type with fields preserved.
func TestDecodeCommandExecutionApprovalRequest(t *testing.T) {
	params, _ := json.Marshal(CommandExecutionApprovalRequest{
		Command:  "ls -la",
		ThreadID: "t1",
		TurnID:   "turn1",
		Cwd:      "/home/user",
	})
	v, err := DecodeServerRequest(MethodItemCommandExecutionRequestApproval, params)
	if err != nil {
		t.Fatalf("DecodeServerRequest: %v", err)
	}
	req, ok := v.(CommandExecutionApprovalRequest)
	if !ok {
		t.Fatalf("expected CommandExecutionApprovalRequest, got %T", v)
	}
	if req.Command != "ls -la" || req.ThreadID != "t1" || req.Cwd != "/home/user" {
		t.Fatalf("unexpected fields: %+v", req)
	}
}

// TestDecodeFileChangeApprovalRequest verifies that the file change approval
// method is decoded into the correct type with fields preserved.
func TestDecodeFileChangeApprovalRequest(t *testing.T) {
	params, _ := json.Marshal(FileChangeApprovalRequest{
		ThreadID:  "t2",
		TurnID:    "turn2",
		GrantRoot: "/tmp",
		Reason:    "needed for build",
	})
	v, err := DecodeServerRequest(MethodItemFileChangeRequestApproval, params)
	if err != nil {
		t.Fatalf("DecodeServerRequest: %v", err)
	}
	req, ok := v.(FileChangeApprovalRequest)
	if !ok {
		t.Fatalf("expected FileChangeApprovalRequest, got %T", v)
	}
	if req.ThreadID != "t2" || req.GrantRoot != "/tmp" || req.Reason != "needed for build" {
		t.Fatalf("unexpected fields: %+v", req)
	}
}

// TestDecodePermissionsApprovalRequest verifies that the permissions approval
// method is decoded into the correct type with fields preserved.
func TestDecodePermissionsApprovalRequest(t *testing.T) {
	params, _ := json.Marshal(PermissionsApprovalRequest{
		ThreadID:    "t3",
		CWD:         "/tmp/w",
		Permissions: json.RawMessage(`{"fileSystem":{"read":["/tmp/x"]}}`),
	})
	v, err := DecodeServerRequest(MethodItemPermissionsRequestApproval, params)
	if err != nil {
		t.Fatalf("DecodeServerRequest: %v", err)
	}
	req, ok := v.(PermissionsApprovalRequest)
	if !ok {
		t.Fatalf("expected PermissionsApprovalRequest, got %T", v)
	}
	// `permissions` is an object upstream, not a list; it is passed through as raw JSON so
	// the shape survives without the SDK modelling the whole profile family.
	if req.ThreadID != "t3" || req.CWD != "/tmp/w" {
		t.Fatalf("unexpected fields: %+v", req)
	}
	if len(req.Permissions) == 0 || req.Permissions[0] != '{' {
		t.Fatalf("permissions profile was not preserved: %s", req.Permissions)
	}
}

// TestDecodeUserInputRequest verifies that the user input request method is
// decoded into the correct type with nested question fields preserved.
func TestDecodeUserInputRequest(t *testing.T) {
	params, _ := json.Marshal(UserInputRequest{
		ThreadID: "t4",
		TurnID:   "turn4",
		Questions: []UserInputQuestion{
			{
				ID:       "q1",
				Header:   "Confirm",
				Question: "Are you sure?",
				Options: []UserInputOption{
					{Label: "Yes", Description: "Proceed"},
					{Label: "No", Description: "Cancel"},
				},
			},
		},
	})
	v, err := DecodeServerRequest(MethodItemToolRequestUserInput, params)
	if err != nil {
		t.Fatalf("DecodeServerRequest: %v", err)
	}
	req, ok := v.(UserInputRequest)
	if !ok {
		t.Fatalf("expected UserInputRequest, got %T", v)
	}
	if req.ThreadID != "t4" || len(req.Questions) != 1 {
		t.Fatalf("unexpected fields: %+v", req)
	}
	if req.Questions[0].ID != "q1" || len(req.Questions[0].Options) != 2 {
		t.Fatalf("unexpected question: %+v", req.Questions[0])
	}
}

// TestDecodeUnknownMethod verifies that an unrecognized method name returns
// ErrUnsupportedServerRequest.
func TestDecodeUnknownMethod(t *testing.T) {
	_, err := DecodeServerRequest("item/unknown/mystery", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnsupportedServerRequest) {
		t.Fatalf("expected ErrUnsupportedServerRequest, got %v", err)
	}
}

// TestDecodeEmptyMethod verifies that an empty method string also returns
// ErrUnsupportedServerRequest.
func TestDecodeEmptyMethod(t *testing.T) {
	_, err := DecodeServerRequest("", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnsupportedServerRequest) {
		t.Fatalf("expected ErrUnsupportedServerRequest for empty method, got %v", err)
	}
}

// TestDecodeMalformedJSONCommandExecution verifies that malformed JSON params
// for a known method return a decode error.
func TestDecodeMalformedJSONCommandExecution(t *testing.T) {
	_, err := DecodeServerRequest(MethodItemCommandExecutionRequestApproval, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error from malformed JSON, got nil")
	}
}

// TestDecodeMalformedJSONFileChange verifies malformed JSON handling for file change.
func TestDecodeMalformedJSONFileChange(t *testing.T) {
	_, err := DecodeServerRequest(MethodItemFileChangeRequestApproval, json.RawMessage(`not-json`))
	if err == nil {
		t.Fatal("expected error from malformed JSON, got nil")
	}
}

// TestDecodeNilParams verifies that nil params decode without panicking.
func TestDecodeNilParams(t *testing.T) {
	_, err := DecodeServerRequest(MethodItemToolRequestUserInput, nil)
	// nil is valid JSON null; json.Unmarshal(nil, &struct{}{}) returns an error
	// because nil is treated as empty slice. We just verify no panic.
	_ = err
}
