// Command gendocs regenerates the API index embedded in docs/api-reference.md,
// llms.txt and llms-full.txt from the module's exported methods.
//
// Those documents used to carry hand-written method lists, which rot the same way the
// README coverage table did. This tool is the single author: it parses the root package
// with go/ast (more robust than scanning text), renders grouped `Client` and
// `SessionThread` method lists, and splices them between named markers.
//
// Usage (from the module root):
//
//	go run ./tools/gendocs           # rewrite the marked regions
//	go run ./tools/gendocs -check    # fail if they are stale (CI)
//
// Markers (each may appear at most once per file; a file may carry any subset):
//
//	<!-- gendocs:client:start -->   ...   <!-- gendocs:client:end -->
//	<!-- gendocs:session:start -->  ...   <!-- gendocs:session:end -->
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// targets are the documents that embed generated regions, relative to the module root.
var targets = []string{
	"docs/api-reference.md",
	"llms.txt",
	"llms-full.txt",
}

type method struct {
	name string
	sig  string // e.g. "func (c *Client) Initialize(ctx context.Context, req InitializeParams) (InitializeResponse, error)"
}

type group struct {
	title   string
	methods []method
}

func main() {
	root := flag.String("root", ".", "module root")
	check := flag.Bool("check", false, "verify the generated regions are up to date instead of writing")
	flag.Parse()

	fset := token.NewFileSet()
	client, session, err := parseRoot(*root, fset)
	if err != nil {
		fatal(err)
	}
	regions := map[string]string{
		"client":  renderClient(client),
		"session": renderSession(session),
	}

	changed := false
	for _, rel := range targets {
		path := filepath.Join(*root, rel)
		orig, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		updated, fileChanged, err := spliceAll(string(orig), regions, rel)
		if err != nil {
			fatal(err)
		}
		if !fileChanged {
			continue
		}
		changed = true
		if *check {
			fmt.Fprintf(os.Stderr, "gendocs: %s is stale\n", rel)
			continue
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("gendocs: regenerated %s\n", rel)
	}
	if *check {
		if changed {
			fmt.Fprintln(os.Stderr, "gendocs: run `go run ./tools/gendocs` and commit the result")
			os.Exit(1)
		}
		fmt.Println("gendocs: all marked regions are up to date")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gendocs:", err)
	os.Exit(1)
}

// parseRoot collects the exported methods of the root package (all .go files in dir
// except _test.go, which cannot be part of the public API).
func parseRoot(dir string, fset *token.FileSet) (client, session []method, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if perr != nil {
			return nil, nil, perr
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			switch receiverName(fset, fn) {
			case "*Client":
				client = append(client, method{fn.Name.Name, sig(fset, fn, "(c *Client)")})
			case "*SessionThread":
				session = append(session, method{fn.Name.Name, sig(fset, fn, "(t *SessionThread)")})
			}
		}
	}
	sortMethods(client)
	sortMethods(session)
	return client, session, nil
}

func receiverName(fset *token.FileSet, fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, fn.Recv.List[0].Type); err != nil {
		return ""
	}
	return b.String()
}

// sig renders "func (recv) Name(params) results". printer handles parameter formatting
// (names, variadics, grouping) so we do not re-implement it.
func sig(fset *token.FileSet, fn *ast.FuncDecl, recv string) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, fn.Type); err != nil {
		return "func " + recv + " " + fn.Name.Name + "(?)"
	}
	// b looks like "func(params) results"; swap the leading "func" for the full head.
	return "func " + recv + " " + fn.Name.Name + strings.TrimPrefix(b.String(), "func")
}

func sortMethods(ms []method) {
	sort.Slice(ms, func(i, j int) bool { return ms[i].name < ms[j].name })
}

// renderClient buckets the client methods into stable, human-meaningful sections.
// Order matters: specific prefixes must precede the generic "Thread" rule.
func renderClient(ms []method) string {
	rules := []struct {
		title string
		match func(string) bool
	}{
		{"Core", inSet("Initialize", "Ping", "Close", "Call")},
		{"Thread sections", hasPrefix("ThreadSection")},
		{"Thread attachments", hasPrefix("ThreadAttachment")},
		{"Thread goals", hasPrefix("ThreadGoal")},
		{"Thread", hasPrefix("Thread")},
		{"Turn", hasPrefix("Turn")},
		{"Account / login", inSet(
			"AccountRead", "Logout", "LoginAPIKey", "LoginChatGPT", "LoginDeviceCode",
			"CancelLoginAccount", "GetAccountRateLimits", "GetAccountTokenUsage",
			"ConsumeAccountRateLimitResetCredit", "GetWorkspaceMessages", "SendAddCreditsNudgeEmail",
			"GatewayOAuthLogin", "GatewayOAuthCancel", "GatewayOAuthRead")},
		{"Models", hasPrefix("Model")},
		{"Config", or(hasPrefix("Config"), inSet("SetModel", "SetApprovalPolicy", "SetSandbox"))},
		{"Review", hasPrefix("Review")},
		{"Skills / experimental features / hooks", or(hasPrefix("Skills"), hasPrefix("ExperimentalFeature"), hasPrefix("Hooks"))},
		{"Plugins / marketplace", or(hasPrefix("Plugin"), hasPrefix("Marketplace"))},
		{"Apps", hasPrefix("Apps")},
		{"Filesystem", hasPrefix("FS")},
		{"MCP", hasPrefix("MCP")},
		{"Command execution", hasPrefix("CommandExec")},
		{"Sessions / events / wait helpers", inSet(
			"StartThread", "ResumeThread", "Events",
			"WaitForTurn", "WaitForFinalAgentMessage", "WaitForStructuredOutput")},
		{"Other", func(string) bool { return true }},
	}
	remaining := map[string]bool{}
	for _, m := range ms {
		remaining[m.name] = true
	}
	var b strings.Builder
	b.WriteString("<!-- Generated by `go run ./tools/gendocs` from the exported API. DO NOT EDIT. -->\n")
	b.WriteString("\nAll methods take `context.Context` as the first argument.\n")
	for _, r := range rules {
		var g group
		g.title = r.title
		for _, m := range ms {
			if remaining[m.name] && r.match(m.name) {
				g.methods = append(g.methods, m)
				delete(remaining, m.name)
			}
		}
		if len(g.methods) == 0 {
			continue
		}
		b.WriteString("\n#### " + g.title + "\n\n```go\n")
		for _, m := range g.methods {
			b.WriteString(m.sig + "\n")
		}
		b.WriteString("```\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSession(ms []method) string {
	var b strings.Builder
	b.WriteString("<!-- Generated by `go run ./tools/gendocs` from the exported API. DO NOT EDIT. -->\n\n```go\n")
	for _, m := range ms {
		b.WriteString(m.sig + "\n")
	}
	b.WriteString("```")
	return b.String()
}

func hasPrefix(p string) func(string) bool {
	return func(s string) bool { return strings.HasPrefix(s, p) }
}

func inSet(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(s string) bool { return set[s] }
}

func or(fs ...func(string) bool) func(string) bool {
	return func(s string) bool {
		for _, f := range fs {
			if f(s) {
				return true
			}
		}
		return false
	}
}

// spliceAll replaces every named region present in doc. It returns the new content and
// whether anything changed. A name with no markers in this file is skipped.
func spliceAll(doc string, regions map[string]string, name string) (string, bool, error) {
	changed := false
	// Deterministic order.
	names := make([]string, 0, len(regions))
	for n := range regions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		start := fmt.Sprintf("<!-- gendocs:%s:start -->", n)
		end := fmt.Sprintf("<!-- gendocs:%s:end -->", n)
		i := strings.Index(doc, start)
		if i < 0 {
			if strings.Contains(doc, end) {
				return "", false, fmt.Errorf("%s: found %q without %q", name, end, start)
			}
			continue // this file does not carry this region
		}
		j := strings.Index(doc, end)
		if j < 0 || j < i {
			return "", false, fmt.Errorf("%s: region %q has no closing marker", name, n)
		}
		next := doc[:i+len(start)] + "\n" + regions[n] + "\n" + doc[j:]
		if next != doc {
			changed = true
		}
		doc = next
	}
	return doc, changed, nil
}
