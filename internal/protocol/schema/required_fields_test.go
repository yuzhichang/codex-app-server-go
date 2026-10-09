package schema

import (
	"encoding/json"
	"testing"
)

// Upstream marks `name` and `version` as required on ClientInfo, meaning the KEYS must be
// present. With omitempty on both, an empty value was dropped from the payload and the server
// rejected the handshake -- a divergence the shape allow-list had recorded as "identical JSON
// representation", which is why it went unnoticed.
//
// The zero value is the interesting case: it is what a caller gets before setting anything, and
// it has to still produce a structurally valid payload.
func TestClientInfoAlwaysSendsRequiredFields(t *testing.T) {
	encoded, err := json.Marshal(ClientInfo{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var sent map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &sent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, required := range []string{"name", "version"} {
		if _, ok := sent[required]; !ok {
			t.Errorf("%q is required upstream but was omitted from %s", required, encoded)
		}
	}
	// `title` is optional and should stay omitted when empty.
	if _, ok := sent["title"]; ok {
		t.Errorf("title is optional and should not be sent when empty: %s", encoded)
	}

	// Values, when set, still go out unchanged.
	encoded, err = json.Marshal(ClientInfo{Name: "test", Title: "T", Version: "v1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var full map[string]string
	if err := json.Unmarshal(encoded, &full); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if full["name"] != "test" || full["title"] != "T" || full["version"] != "v1" {
		t.Fatalf("round-trip lost values: %s", encoded)
	}
}
