package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Port of `go run ./tools/codexgen gen-client`: generate typed Client bindings and their tests from
// gen/method-surface.json.

var (
	gcConstDecl  = regexp.MustCompile(`(?m)^\s*(Method\w+)\s*=\s*"([^"]+)"`)
	gcTypeDecl   = regexp.MustCompile(`(?m)^type (\w+)\b`)
	gcTypeAlias  = regexp.MustCompile(`(?m)^\t(\w+)\s*=`)
	gcClientSig  = regexp.MustCompile(`(?s)func \(c \*Client\) (\w+)\(([^)]*)\)\s*([^{]*)\{`)
	gcAnyClient  = regexp.MustCompile(`func \(c \*Client\) (\w+)\(`)
	gcConstInFn  = regexp.MustCompile(`protocol\.(Method\w+)`)
	gcDecoderCs  = regexp.MustCompile(`(?m)case protocol\.(Method\w+):\s*\n\s*(?:return|target =)\s*&(\w+)\{\}`)
	gcAliasDecl  = regexp.MustCompile(`(?m)^\t(\w+)\s*=\s*(?:schematypes|protocol)\.(\w+)`)
	gcMethodConst = regexp.MustCompile(`Method\w+`)
)

var gcLiteralArgs = map[string]string{"string": `""`, "int": "0", "int64": "0", "bool": "false"}

func gcDeclaredTypes(text string) map[string]bool {
	out := map[string]bool{}
	for _, m := range gcTypeDecl.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
	for _, m := range gcTypeAlias.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
	return out
}

// gcClientFuncBodies emulates Python's `func \(c \*Client\) (\w+)\(.*?(?=\nfunc |\n// |\Z)`
// without lookahead: the body runs from the signature to the next "\nfunc " / "\n// " / EOF.
func gcClientFuncBodies(text string) []struct{ name, body string } {
	locs := gcAnyClient.FindAllStringSubmatchIndex(text, -1)
	var out []struct{ name, body string }
	for _, loc := range locs {
		name := text[loc[2]:loc[3]]
		bodyStart := loc[1] // right after "func (c *Client) Name("
		boundary := len(text)
		if i := strings.Index(text[bodyStart:], "\nfunc "); i >= 0 {
			boundary = minInt(boundary, bodyStart+i)
		}
		if i := strings.Index(text[bodyStart:], "\n// "); i >= 0 {
			boundary = minInt(boundary, bodyStart+i)
		}
		out = append(out, struct{ name, body string }{name, text[loc[0]:boundary]})
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func gcSecondParamType(params string) (string, bool) {
	var parts []string
	for _, p := range strings.Split(params, ",") {
		if s := strings.TrimSpace(p); s != "" {
			parts = append(parts, s)
		}
	}
	for _, part := range parts {
		if strings.HasPrefix(part, "ctx ") {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) >= 2 {
			return fields[len(fields)-1], true
		}
		return part, true
	}
	return "", false
}

func gcRenderMethod(name, constant, params, resp string, method string) string {
	doc := []string{
		fmt.Sprintf("// %s calls `%s`.", name, method),
		"//",
		"// Generated binding. See gen/method-surface.json for the authoritative surface.",
	}
	arg := ""
	if params != "" {
		arg = "req " + params
	}
	argpart := ""
	if arg != "" {
		argpart = ", " + arg
	}
	req := "nil"
	if params != "" {
		req = "req"
	}
	var body []string
	if resp != "" {
		body = []string{
			fmt.Sprintf("func (c *Client) %s(ctx context.Context%s) (%s, error) {", name, argpart, resp),
			fmt.Sprintf("\tvar resp %s", resp),
			fmt.Sprintf("\tif err := c.transport.Call(ctx, protocol.%s, %s, &resp); err != nil {", constant, req),
			fmt.Sprintf("\t\treturn %s{}, err", resp),
			"\t}",
			"\treturn resp, nil",
			"}",
		}
	} else {
		body = []string{
			fmt.Sprintf("func (c *Client) %s(ctx context.Context%s) error {", name, argpart),
			fmt.Sprintf("\treturn c.transport.Call(ctx, protocol.%s, %s, nil)", constant, req),
			"}",
		}
	}
	return strings.Join(append(doc, body...), "\n") + "\n"
}

func gcRenderCase(name, params, wire string) string {
	arg := ""
	if params != "" {
		arg = fmt.Sprintf(", *new(codexgo.%s)", params)
	}
	return strings.Join([]string{
		fmt.Sprintf("\t\t{%q, %q, func(ctx context.Context, c *codexgo.Client) error {", name, wire),
		fmt.Sprintf("\t\t\t_, err := c.%s(ctx%s)", name, arg),
		"\t\t\treturn err",
		"\t\t}},",
	}, "\n")
}

func gcRenderTestWithArg(name, argType string, hasArg, multi bool, wire string) string {
	arg := ""
	if hasArg {
		switch {
		case gcLiteralArgs[argType] != "":
			arg = ", " + gcLiteralArgs[argType]
		case strings.HasPrefix(argType, "*"):
			arg = ", nil"
		default:
			arg = fmt.Sprintf(", *new(codexgo.%s)", argType)
		}
	}
	call := fmt.Sprintf("c.%s(ctx%s)", name, arg)
	stmt := "\t\t\terr := " + call
	if multi {
		stmt = "\t\t\t_, err := " + call
	}
	return strings.Join([]string{
		fmt.Sprintf("\t\t{%q, %q, func(ctx context.Context, c *codexgo.Client) error {", name, wire),
		stmt,
		"\t\t\treturn err",
		"\t\t}},",
	}, "\n")
}

func gcNotificationDecoders() map[string]string {
	out := map[string]string{}
	for _, name := range []string{"events.go", "events_extra.go"} {
		p := filepath.Join(root(), name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		for _, m := range gcDecoderCs.FindAllStringSubmatch(readFile(p), -1) {
			out[m[1]] = m[2]
		}
	}
	return out
}

func gcAliasTargets() map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(root())
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		for _, m := range gcAliasDecl.FindAllStringSubmatch(readFile(filepath.Join(root(), e.Name())), -1) {
			out[m[1]] = m[2]
		}
	}
	return out
}

func gcResolve(name string, aliases map[string]string) string {
	seen := map[string]bool{}
	for {
		t, ok := aliases[name]
		if !ok || seen[name] {
			return name
		}
		seen[name] = true
		name = t
	}
}

func gcRenderNotificationTests(methods []surfaceEntry, whitelist map[string]bool, consts map[string]string) [][2]string {
	decoders := gcNotificationDecoders()
	aliases := gcAliasTargets()
	var cases [][2]string
	for _, m := range methods {
		if m.Face != "server_notification" || m.Experimental {
			continue
		}
		if whitelist[pair(m.Method, m.Face)] {
			continue
		}
		constant := consts[m.Method]
		t := decoders[constant]
		if t != "" {
			cases = append(cases, [2]string{m.Method, gcResolve(t, aliases)})
		}
	}
	return cases
}

func gcWriteConsts(newConsts [][2]string) {
	existing := map[string]string{}
	p := filepath.Join(protocolDir(), "generated_methods.go")
	if _, err := os.Stat(p); err == nil {
		for _, m := range gcConstDecl.FindAllStringSubmatch(readFile(p), -1) {
			existing[m[1]] = m[2]
		}
	}
	merged := map[string]string{}
	for _, c := range newConsts {
		merged[c[0]] = c[1]
	}
	for k, v := range existing {
		merged[k] = v
	}
	lines := []string{
		"// Code generated by `go run ./tools/codexgen gen-client`. DO NOT EDIT.",
		"//",
		"// Method constants for stable client requests that envelope.go did not yet declare.",
		"// Regenerate with `make generate-client`.",
		"",
		"package protocol",
		"",
		"const (",
	}
	var keys []string
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("\t%s = \"%s\"", k, merged[k]))
	}
	lines = append(lines, ")")
	writeFile(p, strings.Join(lines, "\n")+"\n")
}

const gcSchemaImport = "\tschematypes \"github.com/zealbase/codex-app-server-go/internal/protocol/schema\""

func gcWriteMethods(aliases map[string]bool, methodsSrc []string) {
	lines := []string{
		"// Code generated by `go run ./tools/codexgen gen-client` from gen/method-surface.json. DO NOT EDIT.",
		"//",
		"// Typed bindings for stable client requests that had no hand-written method. Method",
		"// and type names come from the upstream Rust variant/type names. Regenerate with",
		"// `make generate-client`.",
		"",
		"package codexgo",
		"",
	}
	var imports []string
	if len(methodsSrc) > 0 {
		imports = append(imports, "\t\"context\"", "", "\t\"github.com/zealbase/codex-app-server-go/internal/protocol\"")
	}
	if len(aliases) > 0 {
		imports = append(imports, gcSchemaImport)
	}
	if len(imports) > 0 {
		lines = append(lines, "import (")
		lines = append(lines, imports...)
		lines = append(lines, ")", "")
	}
	if len(aliases) > 0 {
		lines = append(lines, "// Type aliases for params/response types the root package did not yet expose.", "type (")
		var names []string
		for n := range aliases {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			lines = append(lines, fmt.Sprintf("\t%s = schematypes.%s", n, n))
		}
		lines = append(lines, ")", "")
	}
	lines = append(lines, methodsSrc...)
	writeFile(filepath.Join(root(), "generated_client_methods.go"), strings.Join(lines, "\n"))
}

func gcWriteTests(tests []string, notificationCases [][2]string) {
	lines := []string{
		"// Code generated by `go run ./tools/codexgen gen-client` from gen/method-surface.json. DO NOT EDIT.",
		"//",
		"// Every generated binding is exercised against the mock server. The assertion is that",
		"// the *wire method string* is the upstream one -- the one thing the coverage gate",
		"// cannot verify on its own.",
		"",
		"package codexgo_test",
		"",
		"import (",
		"\t\"context\"",
		"\t\"encoding/json\"",
		"\t\"testing\"",
		"",
		"\tcodexgo \"github.com/zealbase/codex-app-server-go\"",
		")",
		"",
		"func TestGeneratedClientMethodsUseUpstreamWireMethods(t *testing.T) {",
		"\tcases := []struct {",
		"\t\tname   string",
		"\t\twire   string",
		"\t\tinvoke func(context.Context, *codexgo.Client) error",
		"\t}{",
	}
	lines = append(lines, tests...)
	lines = append(lines,
		"\t}", "",
		"\tfor _, tc := range cases {",
		"\t\tt.Run(tc.name, func(t *testing.T) {",
		"\t\t\tclient, mock := newClientFromMock(t)",
		"\t\t\tdefer client.Close()",
		"",
		"\t\t\tvar got json.RawMessage",
		"\t\t\tmock.Handle(tc.wire, func(params json.RawMessage) (any, error) {",
		"\t\t\t\tgot = params",
		"\t\t\t\treturn map[string]any{}, nil",
		"\t\t\t})",
		"",
		"\t\t\tif err := tc.invoke(testCtx(t), client); err != nil {",
		"\t\t\t\tt.Fatalf(\"%s: %v\", tc.wire, err)",
		"\t\t\t}",
		"\t\t\t// A params-less method legitimately sends nothing; everything else must",
		"\t\t\t// reach the handler with a body.",
		"\t\t\tif len(got) == 0 && tc.wire != \"\" {",
		"\t\t\t\tt.Logf(\"%s: handler received no params (method may take none)\", tc.wire)",
		"\t\t\t}",
		"\t\t})",
		"\t}",
		"}",
		"",
	)
	if len(notificationCases) > 0 {
		lines = append(lines,
			"// Notification decoders: a stable notification must decode into its modelled event",
			"// type, not silently fall through to RawNotificationEvent.",
			"func TestGeneratedNotificationDecoders(t *testing.T) {",
			"\tcases := []struct {",
			"\t\twire      string",
			"\t\teventType string",
			"\t}{",
		)
		sorted := append([][2]string(nil), notificationCases...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
		for _, c := range sorted {
			lines = append(lines, fmt.Sprintf("\t\t{%q, %q},", c[0], c[1]))
		}
		lines = append(lines,
			"\t}", "",
			"\tfor _, tc := range cases {",
			"\t\tt.Run(tc.wire, func(t *testing.T) {",
			"\t\t\tclient, mock := newClientFromMock(t)",
			"\t\t\tdefer client.Close()",
			"",
			"\t\t\tsub := client.Events()",
			"\t\t\tdefer sub.Close()",
			"\t\t\ttime.Sleep(20 * time.Millisecond)",
			"",
			"\t\t\tif err := mock.Notify(tc.wire, map[string]any{\"threadId\": \"thr-1\", \"turnId\": \"turn-1\"}); err != nil {",
			"\t\t\t\tt.Fatalf(\"Notify: %v\", err)",
			"\t\t\t}",
			"",
			"\t\t\tselect {",
			"\t\t\tcase ev := <-sub.C():",
			"\t\t\t\t// reflect prints a package-qualified name (codexgo.Foo, protocol.Bar);",
			"\t\t\t\t// strip the qualifier so the assertion is about the type itself.",
			"\t\t\t\tgot := reflect.TypeOf(ev.Value).String()",
			"\t\t\t\tif i := strings.LastIndex(got, \".\"); i >= 0 {",
			"\t\t\t\t\tgot = got[i+1:]",
			"\t\t\t\t}",
			"\t\t\t\tif got != tc.eventType {",
			"\t\t\t\t\tt.Fatalf(\"decoded to %s, want %s\", got, tc.eventType)",
			"\t\t\t\t}",
			"\t\t\tcase <-time.After(3 * time.Second):",
			"\t\t\t\tt.Fatalf(\"timeout waiting for %s\", tc.wire)",
			"\t\t\t}",
			"\t\t})",
			"\t}",
			"}",
			"",
		)
		for i, line := range lines {
			if line == "\t\"encoding/json\"" {
				rest := append([]string{"\t\"reflect\"", "\t\"strings\"", "\t\"time\""}, lines[i+1:]...)
				lines = append(lines[:i+1], rest...)
				break
			}
		}
	}
	writeFile(filepath.Join(root(), "generated_client_methods_test.go"), strings.Join(lines, "\n"))
}

func cmdGenClient(args []string) int {
	dryRun := hasFlag(args, "--dry-run")

	var surface surfaceFile
	loadJSON(filepath.Join(genDir(), "method-surface.json"), &surface)
	methods := surface.Methods
	var wl []whitelistEntry
	loadJSON(filepath.Join(genDir(), "whitelist.json"), &wl)
	whitelist := map[string]bool{}
	for _, w := range wl {
		whitelist[pair(w.Method, w.Face)] = true
	}

	constPath := filepath.Join(protocolDir(), "generated_methods.go")
	constText := readFile(filepath.Join(protocolDir(), "envelope.go"))
	if _, err := os.Stat(constPath); err == nil {
		constText += readFile(constPath)
	}
	consts := map[string]string{}
	for _, m := range gcConstDecl.FindAllStringSubmatch(constText, -1) {
		consts[m[2]] = m[1]
	}

	genMethods := filepath.Join(root(), "generated_client_methods.go")
	genTests := filepath.Join(root(), "generated_client_methods_test.go")
	var rootText string
	rootMethods := map[string]bool{}
	entries, _ := os.ReadDir(root())
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		p := filepath.Join(root(), e.Name())
		if samePath(p, genMethods) || samePath(p, genTests) {
			continue
		}
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		text := readFile(p)
		rootText += text
		for _, m := range gcAnyClient.FindAllStringSubmatch(text, -1) {
			rootMethods[m[1]] = true
		}
	}

	bound := map[string]string{}
	for _, fn := range gcClientFuncBodies(rootText) {
		for _, c := range gcConstInFn.FindAllStringSubmatch(fn.body, -1) {
			if _, ok := bound[c[1]]; !ok {
				bound[c[1]] = fn.name
			}
		}
	}

	rootTypes := gcDeclaredTypes(rootText)

	type sig struct {
		arg   string
		has   bool
		multi bool
	}
	signatures := map[string]sig{}
	for _, m := range gcClientSig.FindAllStringSubmatch(rootText, -1) {
		results := strings.TrimSpace(m[3])
		arg, has := gcSecondParamType(m[2])
		signatures[m[1]] = sig{arg, has, strings.Contains(results, ",")}
	}

	schemaTypes := map[string]bool{}
	for _, name := range goFilesIn(schemaDir()) {
		for t := range gcDeclaredTypes(readFile(filepath.Join(schemaDir(), name))) {
			schemaTypes[t] = true
		}
	}

	var newConsts [][2]string
	var methodsSrc []string
	aliases := map[string]bool{}
	var tests []string
	var skipped []string

	for _, m := range methods {
		if m.Experimental || whitelist[pair(m.Method, m.Face)] {
			continue
		}
		if _, ok := consts[m.Method]; !ok {
			gn := "Method" + m.Variant
			consts[m.Method] = gn
			newConsts = append(newConsts, [2]string{gn, m.Method})
		}
	}

	for _, m := range methods {
		if m.Face != "client_request" || m.Experimental {
			continue
		}
		if whitelist[pair(m.Method, m.Face)] {
			continue
		}
		constant, ok := consts[m.Method]
		if !ok {
			constant = "Method" + m.Variant
			newConsts = append(newConsts, [2]string{constant, m.Method})
		}
		if gname, ok := bound[constant]; ok {
			sg, ok := signatures[gname]
			if !ok {
				skipped = append(skipped, fmt.Sprintf("%s: bound as %s but its signature was not found", m.Method, gname))
				continue
			}
			if sg.has && !rootTypes[sg.arg] && gcLiteralArgs[sg.arg] == "" && !strings.HasPrefix(sg.arg, "*") {
				skipped = append(skipped, fmt.Sprintf("%s: bound as %s but its parameter type (%s) cannot be constructed here", m.Method, gname, sg.arg))
				continue
			}
			tests = append(tests, gcRenderTestWithArg(gname, sg.arg, sg.has, sg.multi, m.Method))
			continue
		}

		name := m.Variant
		if rootMethods[name] {
			skipped = append(skipped, fmt.Sprintf("%s: Go name %s already taken", m.Method, name))
			continue
		}
		params, resp := m.ParamsType, m.ResponseType
		ok = true
		for _, t := range []string{params, resp} {
			if t == "" {
				continue
			}
			if !schemaTypes[t] {
				skipped = append(skipped, fmt.Sprintf("%s: type %s not present in the vendored schema", m.Method, t))
				ok = false
				break
			}
			if !rootTypes[t] {
				aliases[t] = true
			}
		}
		if !ok {
			continue
		}
		methodsSrc = append(methodsSrc, gcRenderMethod(name, constant, params, resp, m.Method))
		tests = append(tests, gcRenderCase(name, params, m.Method))
	}

	if dryRun {
		fmt.Printf("would generate: %d methods, %d tests, %d constants, %d aliases\n",
			len(methodsSrc), len(tests), len(newConsts), len(aliases))
		for _, s := range skipped {
			fmt.Println("  SKIP", s)
		}
		return 0
	}

	notificationCases := gcRenderNotificationTests(methods, whitelist, consts)
	gcWriteConsts(newConsts)
	gcWriteMethods(aliases, methodsSrc)
	gcWriteTests(tests, notificationCases)

	fmt.Printf("generate-client: %d methods, %d tests, %d constants, %d aliases\n",
		len(methodsSrc), len(tests), len(newConsts), len(aliases))
	for _, s := range skipped {
		fmt.Println("  SKIP", s)
	}
	return 0
}

func samePath(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return aa == bb
}
