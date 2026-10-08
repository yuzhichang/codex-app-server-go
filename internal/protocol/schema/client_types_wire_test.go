package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// D2: Capabilities.OptOutNotificationMethods used to be typed `bool`, which made the field
// silently useless -- upstream is Option<Vec<String>> (protocol/v1.rs:65). The type change
// is only meaningful if it both accepts the real wire shape and rejects the old one.
func TestOptOutNotificationMethodsIsAStringList(t *testing.T) {
	encoded, err := json.Marshal(Capabilities{OptOutNotificationMethods: []string{"thread/started"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"optOutNotificationMethods":["thread/started"]`) {
		t.Fatalf("expected a JSON array on the wire, got: %s", encoded)
	}

	var decoded Capabilities
	for _, raw := range []string{
		`{"optOutNotificationMethods":["thread/started","item/agentMessage/delta"]}`,
		`{"optOutNotificationMethods":[]}`,
		`{}`,
	} {
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
	}

	// The legacy (wrong) bool shape must now fail loudly instead of silently succeeding.
	if err := json.Unmarshal([]byte(`{"optOutNotificationMethods":true}`), &decoded); err == nil {
		t.Error("the legacy bool shape must not decode into a []string field")
	}
}

// D5 / D5b: capabilities mirror upstream's Option<InitializeCapabilities>, so a nil
// pointer must omit the field entirely, and ClientInfo must carry `title`.
func TestInitializeRequestCapabilitiesAreOptional(t *testing.T) {
	req := InitializeRequest{ClientInfo: ClientInfo{Name: "n", Title: "My Tool", Version: "1.2.3"}}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "capabilities") {
		t.Errorf("nil capabilities must be omitted from the wire, got: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"title":"My Tool"`) {
		t.Errorf("ClientInfo.title missing from the wire: %s", encoded)
	}

	withCaps, err := json.Marshal(InitializeRequest{
		ClientInfo:   ClientInfo{Name: "n"},
		Capabilities: &Capabilities{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withCaps), `"capabilities":{}`) {
		t.Errorf("non-nil capabilities must be present, got: %s", withCaps)
	}
}

// D5b: the documented defaults must stay all-false. Declaring experimentalApi while
// implementing no experimental surface (R2) would invite notifications we do not handle.
func TestCapabilitiesDefaultsAreAllFalse(t *testing.T) {
	var c Capabilities
	if c.ExperimentalAPI || c.RequestAttestation || c.ExplicitGatewayOauth || c.MCPServerOpenAIFormElicitation {
		t.Errorf("zero-value capabilities must be all-false, got %+v", c)
	}
	if c.OptOutNotificationMethods != nil || c.Extensions != nil {
		t.Errorf("zero-value capabilities must have nil slices/maps, got %+v", c)
	}
}

// D1: the protocol envelope aliases must not carry a jsonrpc field either.
func TestRPCEnvelopeAliasesHaveNoJSONRPCField(t *testing.T) {
	payloads := []any{
		RPCRequest{Method: "thread/start", ID: json.RawMessage("1")},
		RPCNotification{Method: "initialized"},
		RPCResponse{ID: json.RawMessage("1"), Result: json.RawMessage(`{}`)},
	}
	for _, p := range payloads {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal %T: %v", p, err)
		}
		if strings.Contains(string(data), "jsonrpc") {
			t.Errorf("%T must not serialize a jsonrpc field, got: %s", p, data)
		}
	}
}
