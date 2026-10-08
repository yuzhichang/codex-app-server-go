package schema

import (
	"encoding/json"
	"testing"
)

// String-arm unions cannot be a struct with a discriminator -- a struct always encodes as an
// object, which would lose the scalar form -- so they carry their own JSON encoding. These
// pin both directions, because a generator bug here would be invisible: the type would compile
// and simply encode the wrong shape.

// The integer arm must survive a value of ZERO. This is why the scalar arms are pointers:
// with a plain int64 the encoder cannot tell "the integer arm is set to 0" from "no arm is
// set", and would reject a perfectly valid request id.
func TestStringArmUnionIntegerArmZero(t *testing.T) {
	id := RequestIdFromNumber(0)

	encoded, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshalling an integer arm of 0 must be valid: %v", err)
	}
	if string(encoded) != "0" {
		t.Fatalf("encoded = %s, want 0", encoded)
	}

	var back RequestId
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Number == nil || *back.Number != 0 {
		t.Fatalf("round-trip lost the integer arm: %+v", back)
	}
	if back.String != nil {
		t.Fatalf("the string arm was populated from a number: %q", *back.String)
	}
}

// The string arm must survive an empty string, for the same reason.
func TestStringArmUnionStringArmEmpty(t *testing.T) {
	encoded, err := json.Marshal(RequestIdFromString(""))
	if err != nil {
		t.Fatalf("marshalling an empty string arm must be valid: %v", err)
	}
	if string(encoded) != `""` {
		t.Fatalf("encoded = %s, want %q", encoded, "")
	}
}

// Both directions, per arm, matching the wire forms upstream uses.
func TestStringArmUnionRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		value RequestId
		wire  string
		check func(t *testing.T, v RequestId)
	}{
		{
			name:  "string",
			value: RequestIdFromString("req-1"),
			wire:  `"req-1"`,
			check: func(t *testing.T, v RequestId) {
				if v.String == nil || *v.String != "req-1" {
					t.Fatalf("string arm = %+v", v.String)
				}
			},
		},
		{
			name:  "integer",
			value: RequestIdFromNumber(42),
			wire:  `42`,
			check: func(t *testing.T, v RequestId) {
				if v.Number == nil || *v.Number != 42 {
					t.Fatalf("integer arm = %+v", v.Number)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != tc.wire {
				t.Fatalf("encoded = %s, want %s", encoded, tc.wire)
			}
			var back RequestId
			if err := json.Unmarshal([]byte(tc.wire), &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tc.check(t, back)
		})
	}
}

// An array arm keeps its elements, and the token dispatch picks the right arm.
func TestStringArmUnionArrayArm(t *testing.T) {
	filter := ThreadListCwdFilterFromList([]string{"/a", "/b"})
	encoded, err := json.Marshal(filter)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `["/a","/b"]` {
		t.Fatalf("encoded = %s", encoded)
	}

	var stringArm ThreadListCwdFilter
	if err := json.Unmarshal([]byte(`"/only"`), &stringArm); err != nil {
		t.Fatalf("unmarshal string arm: %v", err)
	}
	if stringArm.String == nil || *stringArm.String != "/only" {
		t.Fatalf("string arm = %+v", stringArm.String)
	}
	if stringArm.List != nil {
		t.Fatalf("the array arm was populated from a string: %v", stringArm.List)
	}
}

// Zero arms and two arms are both invalid. Sending nothing, or silently dropping one of two
// set arms, would be worse than an error.
func TestStringArmUnionRejectsWrongArmCount(t *testing.T) {
	if _, err := json.Marshal(RequestId{}); err == nil {
		t.Fatal("expected an error when no arm is set")
	}
	both := RequestIdFromString("x")
	both.Number = new(int64)
	if _, err := json.Marshal(both); err == nil {
		t.Fatal("expected an error when two arms are set")
	}
}

// An arm the SDK does not model must be refused rather than silently dropped: a caller that
// believes it received a value should never be handed nothing.
func TestStringArmUnionRejectsUnmodelledArm(t *testing.T) {
	var v RequestId
	err := json.Unmarshal([]byte(`true`), &v)
	if err == nil {
		t.Fatal("expected an error for an arm that is not modelled")
	}
}
