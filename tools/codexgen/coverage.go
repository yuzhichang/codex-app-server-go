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

// Port of scripts/coverage_gate.py. Coverage is judged against *live wiring evidence in
// real code* plus a committed registry (gen/implemented-methods.json) that must agree with
// that evidence in both directions.

var (
	nonEvidenceFiles    = map[string]bool{"internal/protocol/envelope.go": true}
	nonEvidencePrefixes = []string{"internal/protocol/schema/"}
	decoderFiles        = map[string]bool{"events.go": true, "events_extra.go": true}
	handlerFiles        = map[string]bool{"interaction.go": true}
	clientMethodExclude = func() map[string]bool {
		m := map[string]bool{}
		for k := range decoderFiles {
			m[k] = true
		}
		for k := range handlerFiles {
			m[k] = true
		}
		return m
	}()
	faceKind = map[string]string{
		"client_request":      "client_method",
		"server_request":      "server_request_handler",
		"server_notification": "notification_decoder",
		"client_notification": "client_notification_sender",
	}
)

var (
	constDecl = regexp.MustCompile(`(?m)^\s*(Method\w+)\s*=\s*"([^"]+)"`)
	identRe   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	strRe     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
)

type surfaceMethod struct {
	Method       string `json:"method"`
	Face         string `json:"face"`
	Experimental bool   `json:"experimental"`
}
type surfaceDoc struct {
	Methods []surfaceMethod `json:"methods"`
}
type whitelistEntry struct {
	Method string `json:"method"`
	Face   string `json:"face"`
	Reason string `json:"reason"`
}
type implEntry struct {
	Method string   `json:"method"`
	Face   string   `json:"face"`
	Kind   string   `json:"kind"`
	Wiring []string `json:"wiring"`
	Test   string   `json:"test"`
}
type evidence struct {
	kind   string
	face   string
	wiring []string
	tests  []string
}

type unknownWire struct{ wire, kind string }

const pairSep = "\x00"

func pair(method, face string) string { return method + pairSep + face }
func faceOf(p string) string {
	if i := strings.Index(p, pairSep); i >= 0 {
		return p[i+1:]
	}
	return ""
}
func methodOf(p string) string {
	if i := strings.Index(p, pairSep); i >= 0 {
		return p[:i]
	}
	return p
}

func iterGoFiles() []string {
	var out []string
	_ = filepath.WalkDir(root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			rel, _ := filepath.Rel(root(), path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func loadConsts() map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(protocolDir())
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		text := readFile(filepath.Join(protocolDir(), e.Name()))
		for _, m := range constDecl.FindAllStringSubmatch(text, -1) {
			out[m[2]] = m[1]
		}
	}
	return out
}

// scanFiles returns per-file identifier and string-literal sets (a cheap lexical scan;
// test evidence relies on string literals because the SDK's tests drive mock servers with
// wire method strings rather than referencing the Method* constants).
func scanFiles() (idents, strs map[string]map[string]struct{}) {
	idents = map[string]map[string]struct{}{}
	strs = map[string]map[string]struct{}{}
	for _, rel := range iterGoFiles() {
		text := readFile(rel)
		is := map[string]struct{}{}
		for _, t := range identRe.FindAllString(text, -1) {
			is[t] = struct{}{}
		}
		ss := map[string]struct{}{}
		for _, m := range strRe.FindAllStringSubmatch(text, -1) {
			ss[m[1]] = struct{}{}
		}
		idents[rel] = is
		strs[rel] = ss
	}
	return idents, strs
}

func isEvidenceFile(rel string) bool {
	if nonEvidenceFiles[rel] {
		return false
	}
	if strings.HasSuffix(rel, "_test.go") || rel == "MEMORY.md" || rel == "Makefile" {
		return false
	}
	for _, p := range nonEvidencePrefixes {
		if strings.HasPrefix(rel, p) {
			return false
		}
	}
	return true
}

func evidenceFiles(kind, constant string, idents map[string]map[string]struct{}) []string {
	var pool []string
	for rel := range idents {
		if !isEvidenceFile(rel) {
			continue
		}
		switch kind {
		case "notification_decoder":
			if decoderFiles[rel] {
				pool = append(pool, rel)
			}
		case "server_request_handler":
			if handlerFiles[rel] {
				pool = append(pool, rel)
			}
		case "client_method":
			if !clientMethodExclude[rel] {
				pool = append(pool, rel)
			}
		default: // client_notification_sender -- anywhere a Notify call could live
			pool = append(pool, rel)
		}
	}
	var hits []string
	for _, rel := range pool {
		if _, ok := idents[rel][constant]; ok {
			hits = append(hits, rel)
		}
	}
	sort.Strings(hits)
	return hits
}

func testFilesFor(constant, wire string, testIdents, testStrings map[string]map[string]struct{}) []string {
	var tests []string
	for rel := range testIdents {
		if _, ok := testIdents[rel][constant]; ok {
			tests = append(tests, rel)
			continue
		}
		if _, ok := testStrings[rel][wire]; ok {
			tests = append(tests, rel)
		}
	}
	sort.Strings(tests)
	return tests
}

func buildEvidence(consts map[string]string, faceByWire map[string]string,
	idents, testIdents, testStrings map[string]map[string]struct{}) map[string]*evidence {

	ev := map[string]*evidence{}
	for wire, constant := range consts {
		if face, ok := faceByWire[wire]; ok {
			kind := faceKind[face]
			hits := evidenceFiles(kind, constant, idents)
			if len(hits) == 0 {
				continue
			}
			if kind == "client_notification_sender" {
				notify := regexp.MustCompile(`Notify\([^)]*` + regexp.QuoteMeta(constant))
				found := false
				for _, rel := range hits {
					if notify.MatchString(readFile(rel)) {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			ev[wire] = &evidence{kind: kind, face: face, wiring: hits, tests: testFilesFor(constant, wire, testIdents, testStrings)}
			continue
		}
		// Not declared upstream: classify across roles so the R3 removal report can name
		// what the stale wiring is.
		var kind string
		var hits []string
		for _, cand := range []string{"client_method", "server_request_handler", "notification_decoder"} {
			if h := evidenceFiles(cand, constant, idents); len(h) > 0 {
				kind, hits = cand, h
				break
			}
		}
		if len(hits) == 0 {
			continue
		}
		ev[wire] = &evidence{kind: kind, wiring: hits, tests: testFilesFor(constant, wire, testIdents, testStrings)}
	}
	return ev
}

func notImplementedText(wl []whitelistEntry) string {
	lines := []string{
		"# Declared-stable methods this SDK deliberately does not implement (decision R4).",
		"# Authoritative machine-readable form (with reasons): gen/whitelist.json",
	}
	sorted := append([]whitelistEntry(nil), wl...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Face != sorted[j].Face {
			return sorted[i].Face < sorted[j].Face
		}
		return sorted[i].Method < sorted[j].Method
	})
	for _, w := range sorted {
		lines = append(lines, fmt.Sprintf("%-20s %s", w.Face, w.Method))
	}
	return strings.Join(lines, "\n") + "\n"
}

func cmdCoverage(args []string) int {
	m := mode(args, "report", "write", "check")
	strict := hasFlag(args, "--strict")

	var surface surfaceDoc
	loadJSON(filepath.Join(genDir(), "method-surface.json"), &surface)
	if surface.Methods == nil {
		fmt.Fprintln(os.Stderr, "error: gen/method-surface.json missing; run `make sync`")
		return 2
	}
	var wl []whitelistEntry
	loadJSON(filepath.Join(genDir(), "whitelist.json"), &wl)
	var registered []implEntry
	loadJSON(filepath.Join(genDir(), "implemented-methods.json"), &registered)

	consts := loadConsts()
	idents, strs := scanFiles()
	testIdents := map[string]map[string]struct{}{}
	testStrings := map[string]map[string]struct{}{}
	for rel, v := range idents {
		if strings.HasSuffix(rel, "_test.go") {
			testIdents[rel] = v
		}
	}
	for rel, v := range strs {
		if strings.HasSuffix(rel, "_test.go") {
			testStrings[rel] = v
		}
	}
	faceByWire := map[string]string{}
	surfaceAll := map[string]struct{}{}
	for _, mm := range surface.Methods {
		faceByWire[mm.Method] = mm.Face
		surfaceAll[mm.Method] = struct{}{}
	}
	live := buildEvidence(consts, faceByWire, idents, testIdents, testStrings)

	type decl struct{ method, face string }
	var declared []decl
	declaredSet := map[string]struct{}{}
	for _, mm := range surface.Methods {
		if !mm.Experimental {
			declared = append(declared, decl{mm.Method, mm.Face})
			declaredSet[pair(mm.Method, mm.Face)] = struct{}{}
		}
	}
	whitelistSet := map[string]struct{}{}
	for _, w := range wl {
		whitelistSet[pair(w.Method, w.Face)] = struct{}{}
	}

	var implemented []implEntry
	implementedSet := map[string]struct{}{}
	for _, d := range declared {
		e := live[d.method]
		if e == nil || e.kind != faceKind[d.face] || len(e.tests) == 0 {
			continue
		}
		implemented = append(implemented, implEntry{d.method, d.face, e.kind, e.wiring, e.tests[0]})
		implementedSet[pair(d.method, d.face)] = struct{}{}
	}

	gap := diffSets(declaredSet, whitelistSet, implementedSet)

	type unknownT = unknownWire
	var unknown []unknownT
	for wire, e := range live {
		if _, ok := surfaceAll[wire]; !ok {
			unknown = append(unknown, unknownT{wire, e.kind})
		}
	}
	sort.Slice(unknown, func(i, j int) bool {
		if unknown[i].wire != unknown[j].wire {
			return unknown[i].wire < unknown[j].wire
		}
		return unknown[i].kind < unknown[j].kind
	})

	registeredSet := map[string]struct{}{}
	for _, r := range registered {
		registeredSet[pair(r.Method, r.Face)] = struct{}{}
	}
	unregistered := diffSets(implementedSet, registeredSet)
	stale := diffSets(registeredSet, implementedSet)

	switch m {
	case "write":
		return coverageWrite(implemented, unknown, wl, len(gap))
	case "report":
		return coverageReport(declaredSet, whitelistSet, implementedSet, gap, live, unknown)
	default:
		return coverageCheck(strict, declaredSet, whitelistSet, implementedSet, gap, unknown, unregistered, stale, wl)
	}
}

// diffSets returns a minus the union of others, as sorted pair strings (by method, face).
func diffSets(a map[string]struct{}, others ...map[string]struct{}) []string {
	var out []string
	for k := range a {
		in := false
		for _, o := range others {
			if _, ok := o[k]; ok {
				in = true
				break
			}
		}
		if !in {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if methodOf(out[i]) != methodOf(out[j]) {
			return methodOf(out[i]) < methodOf(out[j])
		}
		return faceOf(out[i]) < faceOf(out[j])
	})
	return out
}

func coverageWrite(implemented []implEntry, unknown []unknownWire, wl []whitelistEntry, gap int) int {
	sort.Slice(implemented, func(i, j int) bool {
		if implemented[i].Face != implemented[j].Face {
			return implemented[i].Face < implemented[j].Face
		}
		return implemented[i].Method < implemented[j].Method
	})
	b, err := json.MarshalIndent(implemented, "", "  ")
	if err != nil {
		fatal(err)
	}
	writeFile(filepath.Join(genDir(), "implemented-methods.json"), string(b)+"\n")

	var ub strings.Builder
	ub.WriteString("# Methods the SDK still wires up that upstream no longer declares (decision R3: remove them).\n")
	for _, u := range unknown {
		ub.WriteString(fmt.Sprintf("%-24s %s\n", u.kind, u.wire))
	}
	writeFile(filepath.Join(genDir(), "unknown-methods.txt"), ub.String())
	writeFile(filepath.Join(genDir(), "not-implemented.txt"), notImplementedText(wl))

	fmt.Printf("write: %d implemented method(s) recorded\n", len(implemented))
	fmt.Printf("       gap=%d unknown(stale upstream)=%d\n", gap, len(unknown))
	return 0
}

func coverageReport(declaredSet, whitelistSet, implementedSet map[string]struct{}, gap []string,
	live map[string]*evidence, unknown []unknownWire) int {

	var untested, notStarted []string
	for _, g := range gap {
		e := live[methodOf(g)]
		if e != nil && e.kind == faceKind[faceOf(g)] {
			untested = append(untested, g)
		} else {
			notStarted = append(notStarted, g)
		}
	}
	byFace := map[string][]string{}
	for _, g := range notStarted {
		byFace[faceOf(g)] = append(byFace[faceOf(g)], methodOf(g))
	}
	fmt.Printf("coverage: declared_stable=%d whitelist=%d implemented=%d gap=%d (not_started=%d wired_but_untested=%d)\n",
		len(declaredSet), len(whitelistSet), len(implementedSet), len(gap), len(notStarted), len(untested))
	fmt.Println("\n  wired but MISSING TEST EVIDENCE (add a test to count these as implemented):")
	for _, u := range untested {
		fmt.Printf("    - %s  (%s)\n", methodOf(u), faceOf(u))
	}
	var faces []string
	for f := range byFace {
		faces = append(faces, f)
	}
	sort.Strings(faces)
	for _, f := range faces {
		fmt.Printf("\n  %s (%d not implemented):\n", f, len(byFace[f]))
		for _, mm := range byFace[f] {
			fmt.Printf("    - %s\n", mm)
		}
	}
	if len(unknown) > 0 {
		fmt.Printf("\n  wires-up-but-not-upstream (%d) -- migrate/remove per R3:\n", len(unknown))
		for _, u := range unknown {
			fmt.Printf("    - %s  (%s)\n", u.wire, u.kind)
		}
	}
	return 0
}

func coverageCheck(strict bool, declaredSet, whitelistSet, implementedSet map[string]struct{},
	gap []string, unknown []unknownWire, unregistered, stale []string, wl []whitelistEntry) int {

	ok := true
	fmt.Println("coverage gate:")
	if len(unregistered) > 0 {
		ok = false
		fmt.Printf("  [FAIL] wiring exists but is not registered (%d): %s\n", len(unregistered), pyPairs(unregistered, 5))
	} else {
		fmt.Println("  [PASS] every wired-up method is registered")
	}
	if len(stale) > 0 {
		ok = false
		fmt.Printf("  [FAIL] registered but no live wiring+test (%d): %s\n", len(stale), pyPairs(stale, 5))
	} else {
		fmt.Println("  [PASS] every registered method has live wiring and a test")
	}
	badWhitelist := diffSets(whitelistSet, declaredSet)
	if len(badWhitelist) > 0 {
		ok = false
		fmt.Printf("  [FAIL] whitelist lists methods that are not declared stable (%d): %s\n", len(badWhitelist), pyPairs(badWhitelist, 5))
	} else {
		fmt.Println("  [PASS] every whitelist entry is a declared-stable method")
	}
	redundant := intersect(whitelistSet, implementedSet)
	if len(redundant) > 0 {
		ok = false
		fmt.Printf("  [FAIL] whitelisted but actually implemented (remove from gen/whitelist.json) (%d): %s\n", len(redundant), pyPairs(redundant, 5))
	} else {
		fmt.Println("  [PASS] no whitelist entry is contradicted by live wiring")
	}
	if len(unknown) > 0 {
		ok = false
		var wires []string
		for _, u := range unknown {
			wires = append(wires, u.wire)
		}
		fmt.Printf("  [FAIL] wired up but no longer declared upstream (remove per R3) (%d): %s\n", len(unknown), pyStringList(wires))
	} else {
		fmt.Println("  [PASS] nothing is wired up that upstream has removed")
	}
	if b, err := os.ReadFile(filepath.Join(genDir(), "not-implemented.txt")); err != nil || string(b) != notImplementedText(wl) {
		ok = false
		fmt.Println("  [FAIL] gen/not-implemented.txt is missing or stale (run `make coverage-write`)")
	} else {
		fmt.Println("  [PASS] gen/not-implemented.txt matches gen/whitelist.json")
	}
	if strict && len(gap) > 0 {
		ok = false
		fmt.Printf("  [FAIL] declared/implemented gap is non-empty (%d); run `report` for the list\n", len(gap))
	} else if len(gap) > 0 {
		fmt.Printf("  [warn] gap non-empty (%d) -- expected mid-implementation; not fatal without --strict\n", len(gap))
	} else {
		fmt.Println("  [PASS] declared_stable - whitelist - implemented == {}")
	}
	fmt.Println()
	fmt.Println("coverage gate: " + passFail(ok))
	fmt.Printf("  (declared_stable=%d whitelist=%d implemented=%d gap=%d)\n",
		len(declaredSet), len(whitelistSet), len(implementedSet), len(gap))
	return boolToCode(ok)
}

func intersect(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if methodOf(out[i]) != methodOf(out[j]) {
			return methodOf(out[i]) < methodOf(out[j])
		}
		return faceOf(out[i]) < faceOf(out[j])
	})
	return out
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}
func boolToCode(ok bool) int {
	if ok {
		return 0
	}
	return 1
}

// pyPairs renders like a Python list of 2-tuples, truncated to n (for FAIL messages).
func pyPairs(pairs []string, n int) string {
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("('%s', '%s')", methodOf(p), faceOf(p))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func pyStringList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = fmt.Sprintf("'%s'", s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func writeFile(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fatal(err)
	}
}
