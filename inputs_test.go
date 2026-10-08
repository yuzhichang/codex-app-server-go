package codexgo_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codexgo "github.com/zealbase/codex-app-server-go"
	"github.com/zealbase/codex-app-server-go/internal/testutil"
)

func TestWithInputsWireFormat(t *testing.T) {
	client, mock := newClientFromMock(t)

	// The upstream shape of each variant, spelled out so the test cannot drift from it.
	type inputItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
		URL  string `json:"url"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	captured := make(chan []inputItem, 1)
	mock.Handle("turn/start", func(params json.RawMessage) (any, error) {
		var wire struct {
			Input []inputItem `json:"input"`
		}
		testutil.MustReadParams(params, &wire)
		select {
		case captured <- wire.Input:
		default:
		}
		return map[string]any{"turn": map[string]any{"id": "t1", "status": "inProgress"}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.TurnStart(ctx, func() codexgo.TurnStartParams {
		req := codexgo.TurnStartParams{ThreadID: "thread-1"}
		codexgo.WithInputs(
			codexgo.TextInput("hello"),
			codexgo.ImageInput("https://example.com/a.png"),
			codexgo.SkillInput("review", "/skills/review"),
			codexgo.MentionInput("README.md", "/repo/README.md"),
		)(&req)
		return req
	}())
	if err != nil {
		t.Fatalf("TurnStart: %v", err)
	}

	select {
	case items := <-captured:
		if len(items) != 4 {
			t.Fatalf("expected 4 input items, got %d: %+v", len(items), items)
		}
		if items[0].Type != "text" || items[0].Text != "hello" {
			t.Fatalf("bad text item: %+v", items[0])
		}
		// image carries {type, url}. It used to also assert a `mediaType`, which is not a
		// field of any UserInput variant -- the assertion is what kept the invented field
		// alive.
		if items[1].Type != "image" || items[1].URL != "https://example.com/a.png" {
			t.Fatalf("bad image item: %+v", items[1])
		}
		// skill requires {name, path} upstream. The old form sent only `name` and this test
		// checked only `name`, so a payload the server rejects passed.
		if items[2].Type != "skill" || items[2].Name != "review" || items[2].Path != "/skills/review" {
			t.Fatalf("bad skill item: %+v", items[2])
		}
		// mention requires {name, path}. The old form sent `id` and `text`, neither of which
		// the variant has.
		if items[3].Type != "mention" || items[3].Name != "README.md" || items[3].Path != "/repo/README.md" {
			t.Fatalf("bad mention item: %+v", items[3])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for turn/start")
	}
}

func TestLocalImageInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pixel.png")
	// 1x1 transparent PNG.
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	}
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatalf("write temp png: %v", err)
	}

	item, err := codexgo.LocalImageInput(path)
	if err != nil {
		t.Fatalf("LocalImageInput: %v", err)
	}
	// item is a typed UserInput now, not a map, so this cannot silently look up a field that
	// does not exist.
	if !strings.HasPrefix(item.URL, "data:image/png;base64,") {
		t.Fatalf("expected png data URI, got %q", item.URL)
	}
	if item.Type != codexgo.UserInputTypeImage {
		t.Fatalf("expected type image, got %q", item.Type)
	}
}

func TestLocalImageInputMissingFile(t *testing.T) {
	if _, err := codexgo.LocalImageInput(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestModelListAndModels(t *testing.T) {
	client, mock := newClientFromMock(t)

	mock.Handle("model/list", func(params json.RawMessage) (any, error) {
		var req struct {
			IncludeHidden bool `json:"includeHidden"`
		}
		testutil.MustReadParams(params, &req)
		data := []map[string]any{
			{"id": "gpt-5", "model": "gpt-5", "hidden": false, "isDefault": true},
		}
		if req.IncludeHidden {
			data = append(data, map[string]any{"id": "gpt-secret", "model": "gpt-secret", "hidden": true})
		}
		return map[string]any{"data": data}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	all, err := client.ModelList(ctx, true)
	if err != nil {
		t.Fatalf("ModelList: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 models with hidden, got %d", len(all))
	}

	visible, err := client.ModelList(ctx, false)
	if err != nil {
		t.Fatalf("ModelList: %v", err)
	}
	if len(visible) != 1 || visible[0].ID != "gpt-5" || !visible[0].IsDefault {
		t.Fatalf("unexpected visible models: %+v", visible)
	}

	ids, err := client.Models(ctx)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(ids) != 1 || ids[0] != "gpt-5" {
		t.Fatalf("unexpected model ids: %+v", ids)
	}
}

func TestTypedEnumOptions(t *testing.T) {
	var req codexgo.TurnStartParams
	codexgo.WithApprovalPolicy(codexgo.ApprovalNever())(&req)
	codexgo.WithSandboxMode(codexgo.SandboxWorkspaceWrite)(&req)
	if req.ApprovalPolicy == nil || req.ApprovalPolicy.Policy() != codexgo.AskForApprovalNever {
		t.Fatalf("approval policy: %+v", req.ApprovalPolicy)
	}
	// The mode is converted to the policy OBJECT the wire requires. The previous assertion
	// pinned "workspace-write", which is the SandboxMode spelling, not the SandboxPolicy
	// discriminator -- the payload was rejected on both the JSON type and the value.
	if req.SandboxPolicy == nil {
		t.Fatal("sandbox policy was not set")
	}
	if req.SandboxPolicy.Type != codexgo.SandboxPolicyTypeWorkspaceWrite {
		t.Fatalf("sandbox policy type: %q, want %q", req.SandboxPolicy.Type, codexgo.SandboxPolicyTypeWorkspaceWrite)
	}
}
