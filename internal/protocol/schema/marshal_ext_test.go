package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The hand-written marshallers below re-list every field of their struct in an `alias` type,
// so a field added to the struct is silently dropped unless the marshaller is updated in
// step. That is exactly how TurnStartParams.Skill became settable-but-never-sent, and the
// WithSkill option that set it appeared to work for as long as nobody checked the wire -- its
// test asserted the local field, not the payload.
//
// This pins the invariant: every field that can be set must reach the wire.
func TestMarshalledParamsSendEveryField(t *testing.T) {
	for _, v := range []any{TurnStartParams{}, TurnSteerParams{}, ThreadStartParams{}} {
		name := reflect.TypeOf(v).Name()
		t.Run(name, func(t *testing.T) {
			typ := reflect.TypeOf(v)
			val := reflect.New(typ).Elem()

			marked := map[string]bool{}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				wireName := strings.Split(field.Tag.Get("json"), ",")[0]
				if wireName == "" || wireName == "-" {
					continue
				}
				mv, ok := markerFor(field.Type)
				if !ok {
					continue // cannot synthesise a non-zero value for this kind
				}
				val.Field(i).Set(mv)
				marked[wireName] = true
			}
			if len(marked) == 0 {
				t.Fatal("no field could be exercised; the test would pass vacuously")
			}

			raw, err := json.Marshal(val.Interface())
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			for wireName := range marked {
				if _, present := decoded[wireName]; !present {
					t.Errorf("field %q is settable but never reaches the wire; add it to the "+
						"MarshalJSON alias, or remove the field\nwire: %s", wireName, raw)
				}
			}
		})
	}
}

// markerFor synthesises a non-zero value, so `omitempty` cannot hide the field.
func markerFor(t reflect.Type) (reflect.Value, bool) {
	const marker = "__marker__"
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf(marker).Convert(t), true
	case reflect.Interface:
		return reflect.ValueOf(marker), true
	case reflect.Slice:
		if t.Elem().Kind() != reflect.String {
			return reflect.Value{}, false
		}
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).SetString(marker)
		return s, true
	case reflect.Ptr:
		if t.Elem().Kind() != reflect.String {
			return reflect.Value{}, false
		}
		p := reflect.New(t.Elem())
		p.Elem().SetString(marker)
		return p, true
	}
	return reflect.Value{}, false
}
