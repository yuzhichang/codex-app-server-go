package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// oObj is a JSON object that preserves key order. The generators iterate `properties` in
// schema order to emit struct fields, and Python's json keeps dict insertion order, so a
// plain Go map (which does not) would reorder fields and change the output bytes.
type oObj struct {
	keys []string
	vals map[string]any
}

func newOObj() *oObj { return &oObj{vals: map[string]any{}} }

func (o *oObj) Len() int                 { return len(o.keys) }
func (o *oObj) Keys() []string           { return o.keys }
func (o *oObj) Get(k string) (any, bool) { v, ok := o.vals[k]; return v, ok }
func (o *oObj) Has(k string) bool        { _, ok := o.vals[k]; return ok }
func (o *oObj) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func parseOrdered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func parseOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return parseOrderedToken(dec, tok)
}

func parseOrderedToken(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := newOObj()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := kt.(string)
				val, err := parseOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(key, val)
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return o, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parseOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return arr, nil
		}
	case string:
		return t, nil
	case json.Number:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected JSON token %v", tok)
}

// --- typed accessors --------------------------------------------------------

func asObj(v any) (*oObj, bool)   { o, ok := v.(*oObj); return o, ok }
func asArr(v any) ([]any, bool)   { a, ok := v.([]any); return a, ok }
func asStr(v any) (string, bool)  { s, ok := v.(string); return s, ok }
func asBool(v any) (bool, bool)   { b, ok := v.(bool); return b, ok }
func getField(v any, k string) (any, bool) {
	if o, ok := v.(*oObj); ok {
		return o.Get(k)
	}
	return nil, false
}
func getStr(v any, k string) (string, bool) {
	if x, ok := getField(v, k); ok {
		return asStr(x)
	}
	return "", false
}

// collectRefs walks a value and returns every "$ref" target of the form "#/definitions/X",
// mirroring the Python `re.findall(r'"#/definitions/([^"]+)"', json.dumps(...))`.
func collectRefs(v any, out map[string]struct{}) {
	switch t := v.(type) {
	case *oObj:
		for _, k := range t.keys {
			if k == "$ref" {
				if s, ok := asStr(t.vals[k]); ok {
					if name, ok := refName(s); ok {
						out[name] = struct{}{}
					}
				}
				continue
			}
			collectRefs(t.vals[k], out)
		}
	case []any:
		for _, e := range t {
			collectRefs(e, out)
		}
	}
}

func refName(ref string) (string, bool) {
	const p = "#/definitions/"
	if len(ref) > len(p) && ref[:len(p)] == p {
		return ref[len(p):], true
	}
	return "", false
}
