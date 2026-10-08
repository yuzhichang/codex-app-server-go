package schema

import (
	"encoding/json"
	"testing"
)

// Some unions have several arms that are all JSON objects. Dispatching on the first byte alone
// cannot tell them apart -- it produced a duplicate `case '{'` -- so these are identified by
// content instead: serde's default "externally tagged" representation gives every object arm a
// single required property whose NAME is the variant tag. These tests pin that, in both
// directions, because a mistake here silently attributes a value to the wrong variant.

func TestExternallyTaggedPicksTheArmByItsTagKey(t *testing.T) {
	cases := []struct {
		name  string
		wire  string
		check func(t *testing.T, v SessionSource)
	}{
		{
			name: "string arm",
			wire: `"cli"`,
			check: func(t *testing.T, v SessionSource) {
				if v.String == nil || *v.String != "cli" {
					t.Fatalf("string arm = %+v", v.String)
				}
				if v.CustomSessionSource != nil || v.SubAgentSessionSource != nil {
					t.Fatalf("an object arm was populated from a string: %+v", v)
				}
			},
		},
		{
			name: "custom object arm",
			wire: `{"custom":"my-source"}`,
			check: func(t *testing.T, v SessionSource) {
				if v.CustomSessionSource == nil {
					t.Fatal("the `custom` key did not select the custom arm")
				}
				if v.CustomSessionSource.Custom != "my-source" {
					t.Fatalf("custom = %q", v.CustomSessionSource.Custom)
				}
				if v.SubAgentSessionSource != nil {
					t.Fatalf("both object arms were populated: %+v", v)
				}
			},
		},
		{
			name: "subAgent object arm",
			wire: `{"subAgent":"review"}`,
			check: func(t *testing.T, v SessionSource) {
				if v.SubAgentSessionSource == nil {
					t.Fatal("the `subAgent` key did not select the sub-agent arm")
				}
				// The nested union decodes by the same rule.
				if v.SubAgentSessionSource.SubAgent.String == nil || *v.SubAgentSessionSource.SubAgent.String != "review" {
					t.Fatalf("nested sub-agent = %+v", v.SubAgentSessionSource.SubAgent)
				}
				if v.CustomSessionSource != nil {
					t.Fatalf("both object arms were populated: %+v", v)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var v SessionSource
			if err := json.Unmarshal([]byte(tc.wire), &v); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.wire, err)
			}
			tc.check(t, v)

			// Whatever came in must go back out unchanged.
			encoded, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != tc.wire {
				t.Fatalf("round-trip: %s -> %s", tc.wire, encoded)
			}
		})
	}
}

// An object carrying none of the tag keys is refused. Guessing an arm would attribute the
// value to the wrong variant, which is worse than an error.
func TestExternallyTaggedRejectsUnknownObject(t *testing.T) {
	var v SessionSource
	if err := json.Unmarshal([]byte(`{"nope":1}`), &v); err == nil {
		t.Fatal("expected an error for an object matching no arm")
	}
}

// The same rule applies to the other externally tagged union.
func TestExternallyTaggedSubAgentSource(t *testing.T) {
	var plain SubAgentSource
	if err := json.Unmarshal([]byte(`"compact"`), &plain); err != nil {
		t.Fatalf("unmarshal string arm: %v", err)
	}
	if plain.String == nil || *plain.String != "compact" {
		t.Fatalf("string arm = %+v", plain.String)
	}

	var other SubAgentSource
	if err := json.Unmarshal([]byte(`{"other":"something"}`), &other); err != nil {
		t.Fatalf("unmarshal other arm: %v", err)
	}
	if other.OtherSubAgentSource == nil || other.OtherSubAgentSource.Other != "something" {
		t.Fatalf("other arm = %+v", other.OtherSubAgentSource)
	}
	if other.ThreadSpawnSubAgentSource != nil {
		t.Fatalf("both object arms were populated: %+v", other)
	}

	// The thread_spawn arm is an inline object, which the generator names so its fields stay
	// typed rather than collapsing to map[string]any.
	var spawn SubAgentSource
	wire := `{"thread_spawn":{"depth":2,"parent_thread_id":"thr-1"}}`
	if err := json.Unmarshal([]byte(wire), &spawn); err != nil {
		t.Fatalf("unmarshal thread_spawn arm: %v", err)
	}
	if spawn.ThreadSpawnSubAgentSource == nil || spawn.ThreadSpawnSubAgentSource.ThreadSpawn == nil {
		t.Fatalf("thread_spawn arm not populated: %+v", spawn)
	}
	inner := spawn.ThreadSpawnSubAgentSource.ThreadSpawn
	if inner.Depth != 2 || inner.ParentThreadID != "thr-1" {
		t.Fatalf("inline object fields were not decoded: %+v", inner)
	}
}
