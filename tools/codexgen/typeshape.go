package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Port of scripts/type_shape_check.py: field-level reconciliation between the SDK's
// hand-written structs and the upstream schema.

var (
	tsStructRe = regexp.MustCompile(`(?ms)^type (\w+) struct \{\n(.*?)^\}`)
	tsFieldRe  = regexp.MustCompile("^(\\w+)\\s+([^\\s`]+)\\s+`([^`]*)`")
)

type tsField struct{ typ, tag string }

func parseStructs(text string) map[string][][3]string {
	out := map[string][][3]string{}
	for _, m := range tsStructRe.FindAllStringSubmatch(text, -1) {
		name, body := m[1], m[2]
		var fields [][3]string
		for _, line := range strings.Split(body, "\n") {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "//") {
				continue
			}
			if fm := tsFieldRe.FindStringSubmatch(s); fm != nil {
				fields = append(fields, [3]string{fm[1], fm[2], fm[3]})
			} else {
				fields = append(fields, [3]string{"<embedded>", s, ""})
			}
		}
		out[name] = fields
	}
	return out
}

func fieldsEqual(a, b [][3]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func nonEmbedded(fields [][3]string) map[string]tsField {
	out := map[string]tsField{}
	for _, f := range fields {
		if f[0] != "<embedded>" {
			out[f[0]] = tsField{f[1], f[2]}
		}
	}
	return out
}

// tsGenerate asks the (in-process) generator what the named definitions should look like.
func tsGenerate(names []string) (map[string][][3]string, error) {
	re := "^(" + strings.Join(names, "|") + ")$"
	dir, err := os.MkdirTemp("", "tsgen")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "generated.go")
	if err := genTypesTo(re, true, false, out, aggregatePath()); err != nil {
		return nil, err
	}
	if gofmt, err := exec.LookPath("gofmt"); err == nil {
		_ = exec.Command(gofmt, "-w", out).Run()
	}
	return parseStructs(readFile(out)), nil
}

func tsDivergences() (map[string][]string, error) {
	hand := parseStructs(readFile(filepath.Join(schemaDir(), "client_types_gen.go")))
	defs := definitionsMap()
	var shared []string
	for n := range hand {
		if _, ok := defs[n]; ok {
			shared = append(shared, n)
		}
	}
	sort.Strings(shared)
	gen, err := tsGenerate(shared)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, name := range shared {
		h := hand[name]
		g, ok := gen[name]
		if !ok || fieldsEqual(h, g) {
			continue
		}
		hf := nonEmbedded(h)
		gf := nonEmbedded(g)
		var notes []string
		for _, k := range sortedKeysDiff(hf, gf) {
			notes = append(notes, fmt.Sprintf("sdk-only         %s %s `%s`  (absent from the aggregate)", k, hf[k].typ, hf[k].tag))
		}
		for _, k := range sortedKeysDiff(gf, hf) {
			notes = append(notes, fmt.Sprintf("aggregate-only   %s %s `%s`", k, gf[k].typ, gf[k].tag))
		}
		for _, k := range sortedCommonKeys(hf, gf) {
			if hf[k] != gf[k] {
				notes = append(notes, fmt.Sprintf("differs          %s: hand=%s upstream=%s", k, hf[k].typ, gf[k].typ))
			}
		}
		if len(notes) > 0 {
			out[name] = notes
		}
	}
	return out, nil
}

func sortedKeysDiff(a, b map[string]tsField) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedCommonKeys(a, b map[string]tsField) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

type tsAllowEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func cmdTypeShape(args []string) int {
	m := mode(args, "report", "check")
	found, err := tsDivergences()
	if err != nil {
		fatal(err)
	}
	var allowList []tsAllowEntry
	loadJSON(filepath.Join(genDir(), "type-shape-allowlist.json"), &allowList)
	allow := map[string]tsAllowEntry{}
	for _, e := range allowList {
		allow[e.Name] = e
	}

	fmt.Printf("type shape check: %d struct(s) diverge from the upstream schema\n", len(found))
	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := allow[name]
		mark := "UNLISTED"
		if ok {
			mark = entry.Status
			if mark == "" {
				mark = "listed"
			}
		}
		fmt.Printf("\n  [%-10s] %s\n", mark, name)
		for _, note := range found[name] {
			fmt.Printf("      %s\n", note)
		}
		if ok {
			fmt.Printf("      reason: %s\n", entry.Reason)
		}
	}

	var unlisted []string
	for _, n := range names {
		if _, ok := allow[n]; !ok {
			unlisted = append(unlisted, n)
		}
	}
	var stale []string
	for _, e := range allowList {
		if _, ok := found[e.Name]; !ok {
			stale = append(stale, e.Name)
		}
	}
	var pending []string
	for _, e := range allowList {
		if e.Status == "pending" {
			if _, ok := found[e.Name]; ok {
				pending = append(pending, e.Name)
			}
		}
	}
	sort.Strings(pending)

	fmt.Printf("\n  allow-listed: %d deliberate, %d pending\n", len(allowList)-len(pending), len(pending))
	if m == "report" {
		return 0
	}
	ok := true
	if len(unlisted) > 0 {
		ok = false
		fmt.Printf("\n  [FAIL] %d diverging struct(s) without an allow-list entry: %s\n", len(unlisted), pyStringList(unlisted))
		fmt.Println("         Either generate the type from the schema, or add it to type-shape-allowlist.json")
		fmt.Println("         with a reason and a status.")
	} else {
		fmt.Println("\n  [PASS] every diverging struct is allow-listed, with a reason")
	}
	if len(stale) > 0 {
		ok = false
		fmt.Printf("  [FAIL] stale allow-list entries (no longer diverge): %s\n", pyStringList(stale))
	}
	fmt.Println("\ntype shape check: " + passFail(ok))
	return boolToCode(ok)
}
