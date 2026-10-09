package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Ports of scripts/type_check.py, scripts/export_check.py and scripts/enum_value_check.py.

func definitionsMap() map[string]json.RawMessage {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(aggregatePath())), &top); err != nil {
		fatal(err)
	}
	for _, key := range []string{"definitions", "$defs"} {
		if raw, ok := top[key]; ok {
			var d map[string]json.RawMessage
			if err := json.Unmarshal(raw, &d); err == nil && d != nil {
				return d
			}
		}
	}
	fatal(fmt.Errorf("no definitions container found in %s", aggregatePath()))
	return nil
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func goFilesIn(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// --- type_check -------------------------------------------------------------

var (
	typeDecl     = regexp.MustCompile(`(?m)^type (\w+)\b`)
	typeBlock    = regexp.MustCompile(`(?ms)^type \(\n(.*?)^\)`)
	typeBlockEnt = regexp.MustCompile(`(?m)^\t(\w+)`)
)

type allowEntry struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func sdkTypeNames() map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range goFilesIn(schemaDir()) {
		text := readFile(filepath.Join(schemaDir(), name))
		for _, m := range typeDecl.FindAllStringSubmatch(text, -1) {
			out[m[1]] = struct{}{}
		}
		for _, blk := range typeBlock.FindAllStringSubmatch(text, -1) {
			for _, e := range typeBlockEnt.FindAllStringSubmatch(blk[1], -1) {
				out[e[1]] = struct{}{}
			}
		}
	}
	return out
}

func cmdTypeCheck(args []string) int {
	m := mode(args, "report", "check")
	defs := definitionsMap()
	sdk := sdkTypeNames()

	allow := map[string]string{}
	var entries []allowEntry
	loadJSON(filepath.Join(genDir(), "type-allowlist.json"), &entries)
	for _, e := range entries {
		allow[e.Name] = e.Reason
	}

	drift := []string{}
	for t := range sdk {
		if _, ok := defs[t]; !ok {
			drift = append(drift, t)
		}
	}
	sort.Strings(drift)
	var unlisted []string
	for _, t := range drift {
		if _, ok := allow[t]; !ok {
			unlisted = append(unlisted, t)
		}
	}
	var stale []string
	for n := range allow {
		if !containsStr(drift, n) {
			stale = append(stale, n)
		}
	}
	sort.Strings(stale)

	fmt.Printf("type check: %d SDK types vs %d upstream definitions\n", len(sdk), len(defs))
	fmt.Printf("  allow-listed   : %d\n", len(allow))
	fmt.Printf("  drifting       : %d (%d unlisted)\n", len(drift), len(unlisted))

	if len(drift) > 0 {
		fmt.Println("\n  drifting types:")
		for _, t := range drift {
			mark := "UNLISTED"
			reason := ""
			if r, ok := allow[t]; ok {
				mark = "listed"
				reason = " -- " + r
			}
			fmt.Printf("    [%-8s] %s%s\n", mark, t, reason)
		}
	}
	if len(stale) > 0 {
		fmt.Printf("\n  [warn] allow-list entries that no longer drift (drop them): %v\n", pyStringList(stale))
	}
	if m == "report" {
		return 0
	}
	ok := true
	if len(unlisted) > 0 {
		ok = false
		fmt.Printf("\n  [FAIL] %d drifting type(s) without an allow-list entry: %v\n", len(unlisted), pyStringList(unlisted))
		fmt.Println("         Either align the name with upstream, remove the type, or add it to")
		fmt.Println("         gen/type-allowlist.json with a reason.")
	} else {
		fmt.Println("\n  [PASS] every SDK type either matches upstream or is allow-listed with a reason")
	}
	if len(stale) > 0 {
		ok = false
		fmt.Printf("  [FAIL] stale allow-list entries: %v\n", pyStringList(stale))
	}
	fmt.Println("\ntype check: " + passFail(ok))
	return boolToCode(ok)
}

// --- export_check -----------------------------------------------------------

var (
	exportAliasEq   = regexp.MustCompile(`(?m)^\t(\w+)\s+=`)
	exportAliasBare = regexp.MustCompile(`(?m)^\t(\w+)\s+[\w.\[\]*]+$`)
	exportTypeDecl  = regexp.MustCompile(`(?m)^type (\w+)`)
	exportField     = regexp.MustCompile("^\\s*(\\w+)\\s+(\\*{0,2})(\\[\\])?(\\*)?([A-Za-z_]\\w*)\\s+`")
	exportStruct    = regexp.MustCompile(`(?ms)^type (\w+) struct \{\n(.*?)^\}`)
)

var exportPublicFiles = []string{
	"types.go",
	"interaction.go",
	"client.go",
	"turn.go",
	"thread.go",
	"events.go",
	"errors.go",
	"internal/protocol/types.go",
	"internal/protocol/defs.go",
}

var exportIgnored = map[string]bool{
	"RawMessage": true, "Error": true, "Time": true, "Duration": true, "Value": true, "Any": true,
	"string": true, "int": true, "int64": true, "float64": true, "bool": true, "byte": true, "rune": true,
}

type exportGap struct{ parent, field, typ, src string }

func exportGaps() []exportGap {
	public := map[string]struct{}{}
	for _, rel := range exportPublicFiles {
		path := filepath.Join(root(), rel)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		text := readFile(path)
		for _, m := range exportAliasEq.FindAllStringSubmatch(text, -1) {
			public[m[1]] = struct{}{}
		}
		for _, m := range exportAliasBare.FindAllStringSubmatch(text, -1) {
			public[m[1]] = struct{}{}
		}
		for _, m := range exportTypeDecl.FindAllStringSubmatch(text, -1) {
			public[m[1]] = struct{}{}
		}
	}
	decls := map[string]string{}
	for _, name := range goFilesIn(schemaDir()) {
		text := readFile(filepath.Join(schemaDir(), name))
		for _, m := range typeDecl.FindAllStringSubmatch(text, -1) {
			if _, ok := decls[m[1]]; !ok {
				decls[m[1]] = name
			}
		}
	}
	var found []exportGap
	for _, name := range goFilesIn(schemaDir()) {
		text := readFile(filepath.Join(schemaDir(), name))
		for _, mm := range exportStruct.FindAllStringSubmatch(text, -1) {
			parent, body := mm[1], mm[2]
			if _, ok := public[parent]; !ok || !strings.HasSuffix(parent, "Params") {
				continue
			}
			for _, line := range strings.Split(body, "\n") {
				fm := exportField.FindStringSubmatch(line)
				if fm == nil {
					continue
				}
				field, stars, slice, ptr, typ := fm[1], fm[2], fm[3], fm[4], fm[5]
				if exportIgnored[typ] {
					continue
				}
				src, ok := decls[typ]
				if !ok {
					continue
				}
				if _, ok := public[typ]; ok {
					continue
				}
				found = append(found, exportGap{parent, field, stars + slice + ptr + typ, src})
			}
		}
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.parent != b.parent {
			return a.parent < b.parent
		}
		if a.field != b.field {
			return a.field < b.field
		}
		return a.typ < b.typ
	})
	return found
}

func cmdExportCheck(args []string) int {
	m := mode(args, "report", "check")
	found := exportGaps()
	if len(found) == 0 {
		fmt.Println("export check: every *Params field type is nameable by a caller")
		return 0
	}
	fmt.Printf("export check: %d *Params field(s) whose type a caller CANNOT name:\n\n", len(found))
	for _, g := range found {
		fmt.Printf("  %s.%s: %s   (declared in %s)\n", g.parent, g.field, g.typ, g.src)
	}
	fmt.Println("\n  Re-export the type in types.go, or the field cannot be set from outside.")
	if m == "check" {
		fmt.Println("\nexport check: FAIL")
		return 1
	}
	return 0
}

// --- enum_value_check -------------------------------------------------------

const (
	minShared = 2
	minShare  = 0.5
)

var (
	sdkEnumDecl = regexp.MustCompile(`(?ms)^type (\w+) string\n\nconst \(\n(.*?)^\)`)
)

func upstreamEnums() map[string]map[string]struct{} {
	defs := definitionsMap()
	out := map[string]map[string]struct{}{}
	for name, raw := range defs {
		var d struct {
			Enum  []any             `json:"enum"`
			OneOf []json.RawMessage `json:"oneOf"`
			AnyOf []json.RawMessage `json:"anyOf"`
		}
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		if len(d.Enum) > 0 {
			allStr := true
			vals := map[string]struct{}{}
			for _, v := range d.Enum {
				s, ok := v.(string)
				if !ok {
					allStr = false
					break
				}
				vals[s] = struct{}{}
			}
			if allStr {
				out[name] = vals
				continue
			}
		}
		for _, arms := range [][]json.RawMessage{d.OneOf, d.AnyOf} {
			if len(arms) == 0 {
				continue
			}
			vals := map[string]struct{}{}
			for _, arm := range arms {
				var a struct {
					Type string   `json:"type"`
					Enum []string `json:"enum"`
				}
				if json.Unmarshal(arm, &a) == nil && a.Type == "string" && len(a.Enum) > 0 {
					for _, s := range a.Enum {
						vals[s] = struct{}{}
					}
				}
			}
			if len(vals) > 0 {
				out[name] = vals
				break
			}
		}
	}
	return out
}

func sdkEnums() map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	for _, file := range goFilesIn(schemaDir()) {
		text := readFile(filepath.Join(schemaDir(), file))
		for _, mm := range sdkEnumDecl.FindAllStringSubmatch(text, -1) {
			name, body := mm[1], mm[2]
			re := regexp.MustCompile(`(?m)^\t` + name + `\w*\s+` + name + ` = "([^"]+)"`)
			vals := map[string]struct{}{}
			for _, v := range re.FindAllStringSubmatch(body, -1) {
				vals[v[1]] = struct{}{}
			}
			if len(vals) > 0 {
				if out[name] == nil {
					out[name] = map[string]struct{}{}
				}
				for v := range vals {
					out[name][v] = struct{}{}
				}
			}
		}
	}
	return out
}

type enumFinding struct {
	name, upstream string
	shared, foreign []string
}

func enumFindings() []enumFinding {
	up := upstreamEnums()
	sdk := sdkEnums()
	var names []string
	for n := range sdk {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []enumFinding
	for _, name := range names {
		values := sdk[name]
		if uvalues, ok := up[name]; ok {
			// Same name upstream: compare the value sets directly. This used to `continue`
			// on the theory that type_check covers it -- but type_check compares type NAMES
			// only, so a value added to a same-named SDK enum was completely invisible.
			var shared, foreign []string
			for v := range values {
				if _, ok := uvalues[v]; ok {
					shared = append(shared, v)
				} else {
					foreign = append(foreign, v)
				}
			}
			if len(foreign) > 0 {
				sort.Strings(shared)
				sort.Strings(foreign)
				out = append(out, enumFinding{name, name, shared, foreign})
			}
			continue
		}
		bestName := ""
		var bestShared map[string]struct{}
		for uname, uvalues := range up {
			shared := map[string]struct{}{}
			for v := range values {
				if _, ok := uvalues[v]; ok {
					shared[v] = struct{}{}
				}
			}
			if len(shared) < minShared || float64(len(shared))/float64(len(values)) < minShare {
				continue
			}
			if bestShared == nil || len(shared) > len(bestShared) {
				bestName, bestShared = uname, shared
			}
		}
		if bestShared != nil {
			var shared, foreign []string
			for v := range bestShared {
				shared = append(shared, v)
			}
			for v := range values {
				if _, ok := up[bestName][v]; !ok {
					foreign = append(foreign, v)
				}
			}
			sort.Strings(shared)
			sort.Strings(foreign)
			out = append(out, enumFinding{name, bestName, shared, foreign})
		}
	}
	return out
}

func cmdEnumValue(args []string) int {
	m := mode(args, "report", "check")
	found := enumFindings()
	if len(found) == 0 {
		fmt.Println("enum value check: no SDK enum carries values upstream does not accept")
		return 0
	}
	fmt.Printf("enum value check: %d SDK enum(s) carry values upstream does not accept:\n\n", len(found))
	for _, f := range found {
		if f.name == f.upstream {
			fmt.Printf("  %s (same name upstream)\n", f.name)
		} else {
			fmt.Printf("  %s ~ upstream `%s`\n", f.name, f.upstream)
		}
		fmt.Printf("      shared : %v\n", pyStringList(f.shared))
		fmt.Printf("      FOREIGN: %v\n", pyStringList(f.foreign))
		if len(f.foreign) > 0 {
			fmt.Println("      -> these values are valid in NO upstream enum here. Either the SDK merged")
			fmt.Println("         two enums, or it invented values the server will reject.")
		} else {
			fmt.Println("      -> an exact subset: the SDK re-invented an existing concept.")
		}
		fmt.Println()
	}
	if m == "check" {
		fmt.Println("enum value check: FAIL")
		return 1
	}
	return 0
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
