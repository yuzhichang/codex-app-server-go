package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Second half of the gen_go_types port: tagged unions, definition emission, and main.

type taggedVariant struct {
	field     string
	tag       string
	props     *oObj
	flattened *oObj
}

func taggedUnion(spec *oObj) []taggedVariant {
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
	var out []taggedVariant
	for _, variant := range variants {
		vo, ok := asObj(variant)
		if !ok {
			return nil
		}
		props, ok := asObj(mustGet(vo, "properties"))
		if !ok {
			return nil
		}
		matched := false
		for _, field := range []string{"type", "kind"} {
			tag, ok := asObj(mustGet(props, field))
			if !ok {
				continue
			}
			enum, ok := asArr(mustGet(tag, "enum"))
			if !ok {
				continue
			}
			var values []string
			for _, e := range enum {
				if s, ok := asStr(e); ok {
					values = append(values, s)
				}
			}
			if len(values) == 1 {
				out = append(out, taggedVariant{field, values[0], props, flattenedProps(vo)})
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
	}
	if len(out) == 0 {
		return nil
	}
	first := out[0].field
	for _, v := range out {
		if v.field != first {
			return nil
		}
	}
	return out
}

func flattenedProps(variant *oObj) *oObj {
	out := newOObj()
	for _, key := range []string{"anyOf", "oneOf"} {
		arms, _ := asArr(mustGet(variant, key))
		for _, arm := range arms {
			ao, ok := asObj(arm)
			if !ok {
				continue
			}
			armProps, ok := asObj(mustGet(ao, "properties"))
			if !ok {
				continue
			}
			for _, field := range armProps.keys {
				if field == "type" {
					continue
				}
				if !out.Has(field) { // setdefault: first writer wins
					out.set(field, armProps.vals[field])
				}
			}
		}
	}
	return out
}

type fieldSig struct {
	typ string
	ok  bool
}

func fieldSignature(spec any, defs map[string]any) fieldSig {
	o, ok := asObj(spec)
	if !ok {
		return fieldSig{}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if arr, ok := asArr(mustGet(o, key)); ok {
			var arms []any
			for _, a := range arr {
				if ao, ok := asObj(a); ok {
					if t, _ := getStr(ao, "type"); t == "null" {
						continue
					}
				}
				arms = append(arms, a)
			}
			if len(arms) == 1 {
				o, ok = asObj(arms[0])
				if !ok {
					return fieldSig{}
				}
				break
			}
			return fieldSig{}
		}
	}
	if t, _ := getStr(o, "type"); t == "null" {
		return fieldSig{}
	}
	return fieldSig{goType(o, defs, false), true}
}

func mergeObjects(flattened, props *oObj) *oObj {
	out := newOObj()
	for _, k := range flattened.keys {
		if props.Has(k) {
			out.set(k, props.vals[k])
		} else {
			out.set(k, flattened.vals[k])
		}
	}
	for _, k := range props.keys {
		if !flattened.Has(k) {
			out.set(k, props.vals[k])
		}
	}
	return out
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func emitTaggedUnion(name string, variants []taggedVariant, defs map[string]any, tagField string) []string {
	type merged_ struct {
		sig fieldSig
		sub any
	}
	merged := map[string]merged_{}
	var order []string
	for _, v := range variants {
		combined := mergeObjects(v.flattened, v.props)
		for _, field := range combined.keys {
			if field == tagField {
				continue
			}
			sub := combined.vals[field]
			sig := fieldSignature(sub, defs)
			if _, ok := merged[field]; !ok {
				order = append(order, field)
				merged[field] = merged_{sig, sub}
			} else if !sameSig(merged[field].sig, sig) {
				merged[field] = merged_{fieldSig{}, sub}
			}
		}
	}

	var variantTags []string
	for _, v := range variants {
		variantTags = append(variantTags, v.tag)
	}
	sortedTags := append([]string(nil), variantTags...)
	sort.Strings(sortedTags)

	lines := []string{
		fmt.Sprintf("// %s mirrors the upstream `%s` definition.", name, name),
		"//",
		fmt.Sprintf("// Upstream declares it as a `%s`-tagged union of %d variants (%s).",
			tagField, len(variants), strings.Join(sortedTags, ", ")),
		fmt.Sprintf("// It is modelled as one flat struct with an explicit %s discriminator", goName(tagField)),
		"// plus the union of every variant's fields, so no field is dropped and no variant is",
		"// rejected.",
		"//",
		"// Fields that disagree in type across variants, or that are themselves unions, are passed",
		"// through as json.RawMessage rather than guessed at.",
		fmt.Sprintf("type %s struct {", name),
		fmt.Sprintf("\t%s string `json:\"%s\"`", goName(tagField), tagField),
	}
	for _, field := range order {
		gofield := "json.RawMessage"
		if merged[field].sig.ok {
			gofield = merged[field].sig.typ
		}
		lines = append(lines, fmt.Sprintf("\t%s %s `json:\"%s,omitempty\"`", goName(field), gofield, field))
	}
	lines = append(lines, "}", "")

	lines = append(lines, fmt.Sprintf("// %s discriminator values, matching the upstream variant tags.", name), "const (")
	width := 0
	for _, tag := range variantTags {
		if l := len(goName(tag)); l > width {
			width = l
		}
	}
	for _, tag := range variantTags {
		constName := fmt.Sprintf("%s%s%s", name, goName(tagField), goName(tag))
		pad := len(constName) + width - len(goName(tag))
		lines = append(lines, fmt.Sprintf("\t%s = \"%s\"", padRight(constName, pad), tag))
	}
	lines = append(lines, ")", "")
	return lines
}

func sameSig(a, b fieldSig) bool {
	if a.ok != b.ok {
		return false
	}
	if !a.ok {
		return true
	}
	return a.typ == b.typ
}

func emitDefinition(name string, spec *oObj, defs map[string]any) []string {
	var out []string
	if enumAny, ok := asArr(mustGet(spec, "enum")); ok {
		allStr := true
		var values []string
		for _, v := range enumAny {
			s, ok := asStr(v)
			if !ok {
				allStr = false
				break
			}
			values = append(values, s)
		}
		if len(values) > 0 && allStr {
			out = append(out,
				fmt.Sprintf("// %s mirrors the upstream `%s` enum.", name, name),
				fmt.Sprintf("type %s string", name),
				"",
				"const (",
			)
			for _, v := range values {
				out = append(out, fmt.Sprintf("\t%s%s %s = \"%s\"", name, goName(v), name, v))
			}
			out = append(out, ")", "")
			return out
		}
	}

	if armDefs := stringArmUnion(spec); armDefs != nil {
		return emitStringArmUnion(name, armDefs, defs)
	}
	if enumValues := stringEnumUnion(spec); enumValues != nil {
		out = append(out,
			fmt.Sprintf("// %s mirrors the upstream `%s` enum.", name, name),
			fmt.Sprintf("type %s string", name),
			"",
			"const (",
		)
		for _, v := range enumValues {
			out = append(out, fmt.Sprintf("\t%s%s %s = \"%s\"", name, goName(v), name, v))
		}
		out = append(out, ")", "")
		return out
	}
	if variantDefs := taggedUnion(spec); variantDefs != nil {
		return emitTaggedUnion(name, variantDefs, defs, variantDefs[0].field)
	}

	props, ok := asObj(mustGet(spec, "properties"))
	if !ok {
		out = append(out,
			fmt.Sprintf("// %s mirrors the upstream `%s` definition.", name, name),
			fmt.Sprintf("type %s = %s", name, goType(spec, defs, false)),
			"",
		)
		return out
	}
	required := stringSet(mustGet(spec, "required"))
	out = append(out,
		fmt.Sprintf("// %s mirrors the upstream `%s` definition.", name, name),
		fmt.Sprintf("type %s struct {", name),
	)
	for _, raw := range props.keys {
		sub := props.vals[raw]
		optional := !required[raw]
		ftype := goType(sub, defs, optional)
		tag := raw
		if optional {
			tag += ",omitempty"
		}
		out = append(out, fmt.Sprintf("\t%s %s `json:\"%s\"`", goName(raw), ftype, tag))
	}
	out = append(out, "}", "")
	return out
}

type surfaceEntry struct {
	Method       string `json:"method"`
	Face         string `json:"face"`
	Variant      string `json:"variant"`
	ParamsType   string `json:"params_type"`
	ResponseType string `json:"response_type"`
	Experimental bool   `json:"experimental"`
}
type surfaceFile struct {
	Methods []surfaceEntry `json:"methods"`
}

func argValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, name+"=") {
			return a[len(name)+1:], true
		}
	}
	return "", false
}

func cmdGenTypes(args []string) int {
	match, hasMatch := argValue(args, "--match")
	fromSurface := hasFlag(args, "--from-surface")
	out, hasOut := argValue(args, "--out")
	schemaPath, hasSchema := argValue(args, "--schema")
	if !hasSchema {
		schemaPath = aggregatePath()
	}
	if !hasOut {
		fmt.Fprintln(os.Stderr, "codexgen gen-types: --out is required")
		return 2
	}
	if !hasMatch && !fromSurface {
		fmt.Fprintln(os.Stderr, "codexgen gen-types: one of --match or --from-surface is required")
		return 2
	}
	shown, n, err := genTypesCore(match, hasMatch, fromSurface, out, schemaPath)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s: %d definitions\n", shown, n)
	return 0
}

// genTypesTo is the generator body, callable in-process (type_shape_check needs it). It is
// intentionally silent; the CLI wrapper prints, so an in-process caller's stdout stays clean.
func genTypesTo(match string, hasMatch, fromSurface bool, out, schemaPath string) error {
	_, _, err := genTypesCore(match, hasMatch, fromSurface, out, schemaPath)
	return err
}

// genTypesCore returns (path shown to the user, definition count, error).
func genTypesCore(match string, hasMatch, fromSurface bool, out, schemaPath string) (string, int, error) {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return "", 0, err
	}
	parsed, err := parseOrdered(data)
	if err != nil {
		return "", 0, err
	}
	schemaObj, _ := asObj(parsed)
	defsObj, _ := asObj(mustGet(schemaObj, "definitions"))
	defs := map[string]any{}
	if defsObj != nil {
		for _, k := range defsObj.keys {
			defs[k] = defsObj.vals[k]
		}
	}

	var rx *regexp.Regexp
	if hasMatch {
		rx, err = regexp.Compile(match)
		if err != nil {
			return "", 0, err
		}
	}

	dest := out
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(root(), dest)
	}

	// Never emit a type the package already declares.
	existing := map[string]bool{}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		p := filepath.Join(filepath.Dir(dest), e.Name())
		abs, _ := filepath.Abs(p)
		dabs, _ := filepath.Abs(dest)
		if abs == dabs {
			continue
		}
		for _, m := range structDeclRe.FindAllStringSubmatch(readFile(p), -1) {
			existing[m[1]] = true
		}
	}

	var roots map[string]bool
	if fromSurface {
		var sf surfaceFile
		loadJSON(filepath.Join(genDir(), "method-surface.json"), &sf)
		wanted := map[string]bool{}
		for _, m := range sf.Methods {
			if m.Experimental {
				continue
			}
			for _, name := range []string{m.ParamsType, m.ResponseType} {
				if name != "" {
					if _, ok := defs[name]; ok {
						wanted[name] = true
					}
				}
			}
			if m.Face == "server_notification" {
				guessed := m.Variant + "Notification"
				if _, ok := defs[guessed]; ok {
					wanted[guessed] = true
				} else if override, ok := notificationTypeOverrides[m.Variant]; ok {
					wanted[override] = true
				}
			}
		}
		roots = wanted
	} else {
		roots = map[string]bool{}
		for n := range defs {
			if rx.MatchString(n) {
				roots[n] = true
			}
		}
	}

	reached := map[string]bool{}
	var pending []string
	for n := range roots {
		reached[n] = true
		pending = append(pending, n)
	}
	for len(pending) > 0 {
		n := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		refs := map[string]struct{}{}
		collectRefs(defs[n], refs)
		for ref := range refs {
			if _, ok := defs[ref]; ok && !reached[ref] {
				reached[ref] = true
				pending = append(pending, ref)
			}
		}
	}

	var wanted []string
	for n := range reached {
		if !existing[n] {
			wanted = append(wanted, n)
		}
	}
	sort.Strings(wanted)

	// Only one gen-types definition set is emitted per process, so a package-level reset is safe.
	extraImports = map[string]bool{}
	body := []string{}
	for _, name := range wanted {
		spec, _ := asObj(defs[name])
		body = append(body, emitDefinition(name, spec, defs)...)
	}

	header := buildGenHeader(match, hasMatch)
	content := strings.Join(append(header, body...), "\n")
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		return "", 0, err
	}
	shown := dest
	if rel, err := filepath.Rel(root(), dest); err == nil && !strings.HasPrefix(rel, "..") {
		shown = rel
	}
	return shown, len(wanted), nil
}

func buildGenHeader(match string, hasMatch bool) []string {
	needed := map[string]bool{"encoding/json": true}
	for k := range extraImports {
		needed[k] = true
	}
	var sorted []string
	for k := range needed {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var importBlock []string
	if len(sorted) == 1 {
		importBlock = []string{fmt.Sprintf("import %q", sorted[0])}
	} else {
		importBlock = []string{"import ("}
		for _, n := range sorted {
			importBlock = append(importBlock, fmt.Sprintf("\t%q", n))
		}
		importBlock = append(importBlock, ")")
	}
	filter := "None"
	if hasMatch {
		filter = match
	}
	head := []string{
		"// Code generated by `go run ./tools/codexgen gen-types` from the vendored codex schema. DO NOT EDIT.",
		"//",
		"// Source: internal/protocol/schema/codex_app_server_protocol.v2.schemas.json",
		fmt.Sprintf("// Filter: %s", filter),
		"//",
		"// Regenerate: make generate-types",
		"",
		"package schema",
		"",
	}
	head = append(head, importBlock...)
	head = append(head, "")
	return head
}
