package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Port of scripts/gen_go_types.py: generate Go types from the vendored codex schema.

var (
	structDeclRe   = regexp.MustCompile(`(?m)^type (\w+)\b`)
	reLowerToUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	reAcronymSplit = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	reNonAlnum     = regexp.MustCompile(`[^0-9A-Za-z]+`)
)

var nilable = map[string]bool{"json.RawMessage": true, "map[string]any": true, "any": true, "struct{}": true}

var notificationTypeOverrides = map[string]string{
	"AuthRecoveryStarted":   "AuthRecoveryNotification",
	"AuthRecoveryCompleted": "AuthRecoveryNotification",
}

var initialisms = map[string]string{
	"id": "ID", "ids": "IDs", "url": "URL", "urls": "URLs", "uri": "URI", "uris": "URIs",
	"api": "API", "http": "HTTP", "json": "JSON", "uuid": "UUID", "cwd": "CWD", "ui": "UI",
	"ip": "IP", "ttl": "TTL",
}

// pyKeywords mirrors Python's keyword.iskeyword over name.lower(). It deliberately omits
// none/true/false: those are case-sensitive keywords (None/True/False), so iskeyword("none")
// is False and the generated "None" must NOT get an underscore suffix.
var pyKeywords = map[string]bool{
	"and": true, "as": true, "assert": true,
	"async": true, "await": true, "break": true, "class": true, "continue": true, "def": true,
	"del": true, "elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true, "is": true,
	"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

func splitWords(raw string) []string {
	spaced := reLowerToUpper.ReplaceAllString(raw, "${1} ${2}")
	spaced = reAcronymSplit.ReplaceAllString(spaced, "${1} ${2}")
	var parts []string
	for _, p := range reNonAlnum.Split(spaced, -1) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func goName(raw string) string {
	var b strings.Builder
	for _, p := range splitWords(raw) {
		if ini, ok := initialisms[strings.ToLower(p)]; ok {
			b.WriteString(ini)
		} else {
			b.WriteString(upperFirst(p))
		}
	}
	name := b.String()
	if name == "" {
		name = "Field"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "N" + name
	}
	if pyKeywords[strings.ToLower(name)] {
		name += "_"
	}
	return name
}

func isStructSpec(spec any) bool {
	if o, ok := asObj(spec); ok {
		if props, ok := o.Get("properties"); ok {
			if _, ok := asObj(props); ok {
				return true
			}
		}
		if t, ok := getStr(o, "type"); ok && t == "object" {
			return true
		}
	}
	return false
}

func goType(spec any, defs map[string]any, optional bool) string {
	o, ok := asObj(spec)
	if !ok {
		return "json.RawMessage"
	}
	if ref, ok := getStr(o, "$ref"); ok {
		name := lastSegment(ref)
		if optional {
			if def, ok := defs[name]; ok && isStructSpec(def) {
				return "*" + name
			}
		}
		return name
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		v := o.vals[key]
		variants, ok := asArr(v)
		if !ok {
			continue
		}
		var nonNull []any
		for _, x := range variants {
			if xo, ok := asObj(x); ok {
				if t, ok := getStr(xo, "type"); ok && t == "null" {
					continue
				}
			}
			nonNull = append(nonNull, x)
		}
		nullable := len(nonNull) != len(variants)
		if len(nonNull) == 1 {
			if _, ok := asObj(nonNull[0]); ok {
				inner := goType(nonNull[0], defs, optional)
				if nullable && !strings.HasPrefix(inner, "*") && !nilable[inner] {
					inner = "*" + inner
				}
				return inner
			}
		}
	}
	// Both genuine unions and the wrapper cases that did not unwrap fall out here.
	if hasUnion(o) {
		return "json.RawMessage"
	}
	if _, ok := o.Get("enum"); ok {
		if en := enumTypeName(o); en != "" {
			return goName(en)
		}
		return "string"
	}
	t, hasType := o.Get("type")
	var ts string
	if hasType {
		if arr, ok := t.([]any); ok {
			ts = ""
			for _, x := range arr {
				if s, ok := x.(string); ok && s != "null" {
					ts = s
					break
				}
			}
		} else if s, ok := t.(string); ok {
			ts = s
		}
	}
	switch ts {
	case "string":
		return "string"
	case "integer":
		return "int64"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		items, _ := o.Get("items")
		return "[]" + goType(items, defs, false)
	case "object":
		addl, _ := o.Get("additionalProperties")
		if ao, ok := asObj(addl); ok {
			if ref, ok := getStr(ao, "$ref"); ok {
				return "map[string]" + lastSegment(ref)
			}
			inner := goType(addl, defs, false)
			if inner != "struct{}" {
				return "map[string]" + inner
			}
		}
		_, hasProps := asObj(mustGet(o, "properties"))
		if !hasProps && addl == nil {
			return "struct{}"
		}
		return "map[string]any"
	}
	if !hasType {
		if _, ok := asObj(mustGet(o, "properties")); ok {
			return "map[string]any"
		}
	}
	return "json.RawMessage"
}

func hasUnion(o *oObj) bool {
	return o.Has("oneOf") || o.Has("anyOf") || o.Has("allOf")
}

func mustGet(o *oObj, k string) any { v, _ := o.Get(k); return v }

func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func enumTypeName(spec *oObj) string {
	if s, ok := getStr(spec, "title"); ok {
		return s
	}
	return ""
}

func stringEnumUnion(spec *oObj) []string {
	var variants []any
	for _, key := range []string{"oneOf", "anyOf"} {
		if arr, ok := asArr(mustGet(spec, key)); ok && len(arr) > 0 {
			variants = arr
			break
		}
	}
	if variants == nil {
		return nil
	}
	var values []string
	for _, v := range variants {
		vo, ok := asObj(v)
		if !ok {
			return nil
		}
		if t, _ := getStr(vo, "type"); t != "string" {
			return nil
		}
		enum, ok := asArr(mustGet(vo, "enum"))
		if !ok {
			return nil
		}
		for _, e := range enum {
			s, ok := asStr(e)
			if !ok {
				return nil
			}
			if !containsStr(values, s) {
				values = append(values, s)
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

type scalarArm struct{ kind, typ string }

func scalarArmOf(spec any) (scalarArm, bool) {
	o, ok := asObj(spec)
	if !ok {
		return scalarArm{}, false
	}
	t, _ := getStr(o, "type")
	switch t {
	case "string":
		return scalarArm{"string", "string"}, true
	case "integer":
		return scalarArm{"integer", "int64"}, true
	case "array":
		items, _ := asObj(mustGet(o, "items"))
		elem := ""
		if items != nil {
			if ref, ok := getStr(items, "$ref"); ok {
				elem = lastSegment(ref)
			} else if s, ok := getStr(items, "type"); ok {
				elem = s
			}
		}
		if elem == "" {
			return scalarArm{}, false
		}
		builtin := map[string]string{"string": "string", "integer": "int64", "number": "float64", "boolean": "bool"}
		if b, ok := builtin[elem]; ok {
			return scalarArm{"array", "[]" + b}, true
		}
		return scalarArm{"array", "[]" + goName(elem)}, true
	}
	return scalarArm{}, false
}

func stringArmUnion(spec *oObj) []any {
	var variants []any
	for _, key := range []string{"oneOf", "anyOf"} {
		if arr, ok := asArr(mustGet(spec, key)); ok && len(arr) > 0 {
			variants = arr
			break
		}
	}
	if variants == nil {
		return nil
	}
	allObjProps := true
	for _, v := range variants {
		if _, scalar := scalarArmOf(v); scalar {
			allObjProps = false
			break
		}
		vo, ok := asObj(v)
		if !ok {
			allObjProps = false
			break
		}
		if _, ok := vo.Get("properties"); !ok {
			allObjProps = false
			break
		}
	}
	if allObjProps {
		return nil
	}

	groups := map[string][]any{}
	var groupOrder []string
	for _, v := range variants {
		token := "object"
		if sa, ok := scalarArmOf(v); ok {
			switch sa.kind {
			case "string":
				token = "string"
			case "integer":
				token = "number"
			default:
				token = "array"
			}
		}
		if _, ok := groups[token]; !ok {
			groupOrder = append(groupOrder, token)
		}
		groups[token] = append(groups[token], v)
	}
	for _, token := range groupOrder {
		arms := groups[token]
		if len(arms) < 2 {
			continue
		}
		if token != "object" {
			return nil
		}
		var tags []map[string]bool
		for _, arm := range arms {
			ao, _ := asObj(arm)
			req, _ := asArr(mustGet(ao, "required"))
			if len(req) == 0 {
				return nil
			}
			set := map[string]bool{}
			for _, r := range req {
				if s, ok := asStr(r); ok {
					set[s] = true
				}
			}
			tags = append(tags, set)
		}
		for i := 0; i < len(tags); i++ {
			for j := i + 1; j < len(tags); j++ {
				for k := range tags[i] {
					if tags[j][k] {
						return nil
					}
				}
			}
		}
	}
	for _, v := range variants {
		if _, ok := scalarArmOf(v); ok {
			continue
		}
		vo, ok := asObj(v)
		if !ok {
			return nil
		}
		if _, ok := getStr(vo, "$ref"); ok {
			continue
		}
		if _, ok := getStr(vo, "title"); ok {
			if _, ok := asObj(mustGet(vo, "properties")); ok {
				continue
			}
		}
		return nil
	}
	return variants
}

var extraImports = map[string]bool{}

func emitInlineStruct(name string, spec *oObj, defs map[string]any, note string) []string {
	required := stringSet(mustGet(spec, "required"))
	out := []string{
		fmt.Sprintf("// %s is the inline object %s, named so its fields stay typed.", name, note),
		fmt.Sprintf("type %s struct {", name),
	}
	props, _ := asObj(mustGet(spec, "properties"))
	if props != nil {
		for _, raw := range props.keys {
			sub := props.vals[raw]
			optional := !required[raw]
			tag := raw
			if optional {
				tag += ",omitempty"
			}
			out = append(out, fmt.Sprintf("\t%s %s `json:\"%s\"`", goName(raw), goType(sub, defs, optional), tag))
		}
	}
	out = append(out, "}", "")
	return out
}

func emitObjectArm(title string, arm *oObj, defs map[string]any) []string {
	required := stringSet(mustGet(arm, "required"))
	var out []string
	type field struct {
		raw, typ string
		optional bool
	}
	var fields []field
	props, _ := asObj(mustGet(arm, "properties"))
	if props != nil {
		for _, raw := range props.keys {
			sub := props.vals[raw]
			optional := !required[raw]
			if so, ok := asObj(sub); ok {
				if _, ok := asObj(mustGet(so, "properties")); ok {
					nested := title + goName(raw)
					out = append(out, emitInlineStruct(nested, so, defs, fmt.Sprintf("on %s.%s", title, raw))...)
					fields = append(fields, field{raw, "*" + nested, optional})
					continue
				}
			}
			fields = append(fields, field{raw, goType(sub, defs, optional), optional})
		}
	}
	out = append(out, fmt.Sprintf("// %s is the object arm of its union.", title), fmt.Sprintf("type %s struct {", title))
	for _, f := range fields {
		tag := f.raw
		if f.optional {
			tag += ",omitempty"
		}
		out = append(out, fmt.Sprintf("\t%s %s `json:\"%s\"`", goName(f.raw), f.typ, tag))
	}
	out = append(out, "}", "")
	return out
}

func emitStringArmUnion(name string, variants []any, defs map[string]any) []string {
	extraImports["bytes"] = true
	extraImports["errors"] = true
	extraImports["fmt"] = true

	type fld struct{ field, gotype string }
	var fields []fld
	var kinds []string
	var nested []string
	add := func(field, gotype, described string) {
		for _, f := range fields {
			if f.field == field {
				return
			}
		}
		fields = append(fields, fld{field, gotype})
		kinds = append(kinds, described)
	}
	for _, v := range variants {
		vo, _ := asObj(v)
		if sa, ok := scalarArmOf(v); ok {
			switch sa.kind {
			case "string":
				add("String", "*string", "a string")
			case "integer":
				add("Number", "*int64", "an integer")
			default:
				add("List", sa.typ, "an array")
			}
			continue
		}
		if ref, ok := getStr(vo, "$ref"); ok {
			r := goName(lastSegment(ref))
			add(r, "*"+r, "a "+r)
			continue
		}
		title, _ := getStr(vo, "title")
		title = goName(title)
		nested = append(nested, emitObjectArm(title, vo, defs)...)
		add(title, "*"+title, "a "+title)
	}

	var out []string
	out = append(out, nested...)
	out = append(out,
		fmt.Sprintf("// %s is a union of %s.", name, strings.Join(kinds, ", ")),
		"//",
		"// Upstream declares it with at least one NON-object arm, so it cannot be modelled as a",
		"// struct with a discriminator the way the tagged unions are: a struct always encodes as",
		"// an object, which would lose the scalar form entirely. It therefore carries its own JSON",
		"// encoding. Exactly one arm is set; use the From... constructors.",
		fmt.Sprintf("type %s struct {", name),
	)
	for _, f := range fields {
		out = append(out, fmt.Sprintf("\t%s %s", f.field, f.gotype))
	}
	out = append(out, "}", "")

	for _, f := range fields {
		arg := f.gotype
		if strings.HasPrefix(arg, "*") {
			arg = arg[1:]
		}
		out = append(out, fmt.Sprintf("// %sFrom%s builds the %s arm.", name, f.field, f.field))
		if strings.HasPrefix(f.gotype, "*") {
			out = append(out, fmt.Sprintf("func %sFrom%s(v %s) %s { return %s{%s: &v} }", name, f.field, arg, name, name, f.field))
		} else {
			out = append(out, fmt.Sprintf("func %sFrom%s(v %s) %s { return %s{%s: v} }", name, f.field, arg, name, name, f.field))
		}
		out = append(out, "")
	}

	isSet := func(f fld) string {
		if strings.HasPrefix(f.gotype, "*") {
			return fmt.Sprintf("u.%s != nil", f.field)
		}
		return fmt.Sprintf("len(u.%s) > 0", f.field)
	}
	out = append(out,
		"// MarshalJSON encodes whichever arm is set, matching the upstream wire form.",
		fmt.Sprintf("func (u %s) MarshalJSON() ([]byte, error) {", name),
		"\tset := 0",
	)
	for _, f := range fields {
		out = append(out, fmt.Sprintf("\tif %s {", isSet(f)), "\t\tset++", "\t}")
	}
	out = append(out,
		"\tif set != 1 {",
		fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s: exactly one arm must be set, got %%d\", set)", name),
		"\t}",
	)
	for _, f := range fields {
		out = append(out, fmt.Sprintf("\tif %s {", isSet(f)), fmt.Sprintf("\t\treturn json.Marshal(u.%s)", f.field), "\t}")
	}
	out = append(out, "\treturn nil, errors.New(\"unreachable\")", "}", "")

	out = append(out,
		"// UnmarshalJSON selects the arm by JSON token: a quoted string, a number, an array or",
		"// an object.",
		fmt.Sprintf("func (u *%s) UnmarshalJSON(data []byte) error {", name),
		"\ttrimmed := bytes.TrimSpace(data)",
		"\tif len(trimmed) == 0 {",
		fmt.Sprintf("\t\treturn fmt.Errorf(\"%s: empty payload\")", name),
		"\t}",
	)

	var objects []*oObj
	var objectTags = map[string]string{}
	for _, v := range variants {
		if _, ok := scalarArmOf(v); ok {
			continue
		}
		vo, _ := asObj(v)
		if _, ok := getStr(vo, "$ref"); ok {
			continue
		}
		objects = append(objects, vo)
	}
	for _, vo := range objects {
		req, _ := asArr(mustGet(vo, "required"))
		if len(req) > 0 {
			if s, ok := asStr(req[0]); ok {
				if title, ok := getStr(vo, "title"); ok {
					objectTags[goName(title)] = s
				}
			}
		}
	}
	multiObject := len(objects) > 1
	if multiObject {
		out = append(out,
			"\t// Several arms are JSON objects; each is identified by its own required",
			"\t// property, so probe for those keys rather than guessing by token alone.",
			"\tvar probe map[string]json.RawMessage",
		)
	}
	out = append(out, "\tswitch trimmed[0] {")

	type branch struct{ token, field, body string }
	var branches []branch
	for _, f := range fields {
		switch {
		case f.gotype == "*string":
			branches = append(branches, branch{`"`, f.field, fmt.Sprintf("\t\tvar v string\n\t\tif err := json.Unmarshal(data, &v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tu.%s = &v", f.field)})
		case f.gotype == "*int64":
			branches = append(branches, branch{"0-9", f.field, fmt.Sprintf("\t\tvar v int64\n\t\tif err := json.Unmarshal(data, &v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tu.%s = &v", f.field)})
		case strings.HasPrefix(f.gotype, "[]"):
			branches = append(branches, branch{"[", f.field, fmt.Sprintf("\t\tvar v %s\n\t\tif err := json.Unmarshal(data, &v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tu.%s = v", f.gotype, f.field)})
		default:
			inner := f.gotype[1:]
			tag, hasTag := objectTags[f.field]
			if hasTag && len(objects) > 1 {
				branches = append(branches, branch{"{", f.field, fmt.Sprintf("\t\tif _, ok := probe[\"%s\"]; ok {\n\t\t\tvar v %s\n\t\t\tif err := json.Unmarshal(data, &v); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tu.%s = &v\n\t\t\treturn nil\n\t\t}", tag, inner, f.field)})
			} else {
				branches = append(branches, branch{"{", f.field, fmt.Sprintf("\t\tvar v %s\n\t\tif err := json.Unmarshal(data, &v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\tu.%s = &v", inner, f.field)})
			}
		}
	}

	modelled := map[string]bool{}
	objectGroupEmitted := false
	for _, br := range branches {
		if br.token == "{" && multiObject {
			if objectGroupEmitted {
				continue
			}
			objectGroupEmitted = true
			out = append(out,
				"\tcase '{':",
				"\t\tif err := json.Unmarshal(data, &probe); err != nil {",
				"\t\t\treturn err",
				"\t\t}",
			)
			for _, b2 := range branches {
				if b2.token != "{" {
					continue
				}
				out = append(out, fmt.Sprintf("\t\t// %s arm", b2.field))
				out = append(out, strings.Split(b2.body, "\n")...)
			}
			out = append(out,
				"\t\t// No required key matched. Refusing is deliberate: guessing an arm",
				"\t\t// would attribute the value to the wrong variant.",
				fmt.Sprintf("\t\treturn fmt.Errorf(\"%s: no object arm matches %%s\", trimmed)", name),
			)
			modelled[br.token] = true
			continue
		}
		if br.token == "0-9" {
			out = append(out,
				"\tcase '-', '+':",
				"\t\tfallthrough",
				"\tcase '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':",
			)
		} else {
			out = append(out, fmt.Sprintf("\tcase %s:", pyReprRune(br.token)))
		}
		out = append(out, strings.Split(br.body, "\n")...)
		out = append(out, "\t\treturn nil")
		modelled[br.token] = true
	}
	out = append(out,
		"\tdefault:",
		"\t\t// No modelled arm matches. Refusing is deliberate: keeping nothing would drop a",
		"\t\t// value the caller believes it received.",
		fmt.Sprintf("\t\treturn fmt.Errorf(\"%s: unsupported arm %%s\", trimmed)", name),
		"\t}",
		"}",
		"",
	)
	return out
}

// pyReprRune renders a single-character string the way Python's repr does (always quotes a
// character that came from a token), e.g. `"` becomes `'"'` and `[` becomes `'['`.
func pyReprRune(ch string) string {
	if ch == `"` {
		return `'"'`
	}
	if ch == `'` {
		return `"'"` // not expected, but keep it valid Go
	}
	return "'" + ch + "'"
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	if arr, ok := asArr(v); ok {
		for _, e := range arr {
			if s, ok := asStr(e); ok {
				out[s] = true
			}
		}
	}
	return out
}
