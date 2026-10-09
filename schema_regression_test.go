package codexgo_test

import (
	"encoding/json"
	"testing"

	codexgo "github.com/zealbase/codex-app-server-go"
)

// Upstream sends integer Unix seconds for thread timestamps, so thread/revert's response must
// decode them. The schema Thread used to type createdAt/updatedAt as *time.Time, which made
// every thread/revert response fail to decode.
func TestThreadRevertResponseDecodesIntegerTimestamps(t *testing.T) {
	raw := `{"thread":{"id":"thr-1","createdAt":1700000000,"updatedAt":1700000001}}`
	var resp codexgo.ThreadRevertResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("thread/revert response failed to decode: %v", err)
	}
	if resp.Thread.ID != "thr-1" {
		t.Fatalf("thread id = %q", resp.Thread.ID)
	}
	if resp.Thread.CreatedAt != 1700000000 || resp.Thread.UpdatedAt != 1700000001 {
		t.Fatalf("timestamps = %d/%d, want the integer seconds verbatim", resp.Thread.CreatedAt, resp.Thread.UpdatedAt)
	}
}

// A required field of the active variant must survive an empty value. `text` is required by
// the text arm, so a flat struct with omitempty would drop it and the server would reject the
// payload.
func TestTextInputKeepsEmptyText(t *testing.T) {
	b, err := json.Marshal(codexgo.TextInput(""))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"type":"text","text":""}` {
		t.Fatalf("empty text input encoded as %s; upstream requires the text key to be present", b)
	}
}
