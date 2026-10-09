package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Port of scripts/codex_schema_surface.py. `sync` needs a codex checkout and the `zstd`
// command-line tool (the Python version needed the `zstandard` module); `verify` and
// `diff-cli` need neither.

const (
	aggregatedSchema = "codex_app_server_protocol.v2.schemas.json"
	sourceRel        = "codex-rs/app-server-protocol/src/protocol/common.rs"
	precomputedDir   = "codex-rs/app-server-protocol/schema/precomputed"
	aggregatedJSON   = "codex-rs/app-server-protocol/schema/json/" + aggregatedSchema
)

var faces = []string{"client_request", "server_request", "server_notification", "client_notification"}

var blockMarkers = []struct{ face, marker string }{
	{"client_request", "client_request_definitions! {"},
	{"server_request", "server_request_definitions! {"},
	{"server_notification", "server_notification_definitions! {"},
	{"client_notification", "client_notification_definitions! {"},
}

func genDirPath() string     { return filepath.Join(root(), "gen") }
func vendorDir() string      { return filepath.Join(root(), ".codex-schema") }
func exportsDir() string     { return filepath.Join(vendorDir(), "exports") }
func schemaDirPath() string  { return filepath.Join(root(), "internal", "protocol", "schema") }

type exportExclusion struct {
	Method string `json:"method"`
	Face   string `json:"face"`
	Reason string `json:"reason"`
}

var exportExclusions = []exportExclusion{
	{"getAuthStatus", "client_request", "v1 deprecated top-level method; not emitted by the export"},
	{"getConversationSummary", "client_request", "v1 deprecated top-level method; not emitted by the export"},
	{"gitDiffToRemote", "client_request", "v1 deprecated top-level method; not emitted by the export"},
	{"rawResponse/completed", "server_notification", "internal-only (common.rs:1979 'This event is internal-only')"},
	{"rawResponseItem/completed", "server_notification", "internal-only (common.rs:1977 'This event is internal-only')"},
}

type methodEntry struct {
	Method               string  `json:"method"`
	Face                 string  `json:"face"`
	Variant              string  `json:"variant"`
	Experimental         bool    `json:"experimental"`
	ExperimentalReason   *string `json:"experimental_reason"`
	ParamsType           *string `json:"params_type"`
	ResponseType         *string `json:"response_type"`
	InExportStable       bool    `json:"in_export_stable"`
	InExportExperimental bool    `json:"in_export_experimental"`
	ExportExcluded       bool    `json:"export_excluded"`
}

type byFace struct {
	ClientRequest      int `json:"client_request"`
	ServerRequest      int `json:"server_request"`
	ServerNotification int `json:"server_notification"`
	ClientNotification int `json:"client_notification"`
}

func (b *byFace) set(face string, v int) {
	switch face {
	case "client_request":
		b.ClientRequest = v
	case "server_request":
		b.ServerRequest = v
	case "server_notification":
		b.ServerNotification = v
	case "client_notification":
		b.ClientNotification = v
	}
}

type methodSet struct {
	ClientRequest      []string `json:"client_request"`
	ServerRequest      []string `json:"server_request"`
	ServerNotification []string `json:"server_notification"`
	ClientNotification []string `json:"client_notification"`
}

func (m *methodSet) set(face string, v []string) {
	switch face {
	case "client_request":
		m.ClientRequest = v
	case "server_request":
		m.ServerRequest = v
	case "server_notification":
		m.ServerNotification = v
	case "client_notification":
		m.ClientNotification = v
	}
}
func (m methodSet) get(face string) []string {
	switch face {
	case "client_request":
		return m.ClientRequest
	case "server_request":
		return m.ServerRequest
	case "server_notification":
		return m.ServerNotification
	default:
		return m.ClientNotification
	}
}

// orderedMap marshals a fixed-key string map in insertion order (Python dicts preserve it).
type orderedMap struct {
	keys []string
	vals map[string]string
}

func newOrderedMap() *orderedMap { return &orderedMap{vals: map[string]string{}} }
func (m *orderedMap) set(k, v string) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}
func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m.vals[k])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// --- source parsing ---------------------------------------------------------

var (
	reAttr         = regexp.MustCompile(`(?s)^#\[(.*?)\]\s*(.*)$`)
	reExperimental = regexp.MustCompile(`^experimental\("([^"]+)"\)`)
	reRename       = regexp.MustCompile(`rename\s*=\s*"([^"]+)"`)
	reStrum        = regexp.MustCompile(`serialize\s*=\s*"([^"]+)"`)
	reVariant      = regexp.MustCompile(`^([A-Z][A-Za-z0-9_]*)\s*(?:=>\s*"([^"]+)")?\s*[({]`)
	reVariantBare  = regexp.MustCompile(`^([A-Z][A-Za-z0-9_]*)\s*,`)
	reParams       = regexp.MustCompile(`\bparams\s*:\s*(.+)`)
	reResponse     = regexp.MustCompile(`\bresponse\s*:\s*(.+)`)
	reAttrInline   = regexp.MustCompile(`#\[.*?\]`)
	reWord         = regexp.MustCompile(`(\w+)`)
)

var attrWords = map[string]bool{"optional": true, "nullable": true, "inline": true, "default": true, "undefined": true, "skip_serializing_if": true}

func blankNoise(text string) string {
	chars := []byte(text)
	i, n := 0, len(text)
	for i < n {
		c := text[i]
		if c == '/' && i+1 < n && text[i+1] == '/' {
			for i < n && text[i] != '\n' {
				chars[i] = ' '
				i++
			}
			continue
		}
		if c == '/' && i+1 < n && text[i+1] == '*' {
			chars[i], chars[i+1] = ' ', ' '
			i += 2
			for i < n && !(text[i] == '*' && i+1 < n && text[i+1] == '/') {
				if text[i] != '\n' {
					chars[i] = ' '
				}
				i++
			}
			if i < n {
				chars[i] = ' '
				if i+1 < n {
					chars[i+1] = ' '
				}
				i += 2
			}
			continue
		}
		if c == '"' {
			chars[i] = ' '
			i++
			for i < n {
				if text[i] == '\\' {
					chars[i] = ' '
					if i+1 < n {
						chars[i+1] = ' '
					}
					i += 2
					continue
				}
				if text[i] == '"' {
					chars[i] = ' '
					i++
					break
				}
				if text[i] != '\n' {
					chars[i] = ' '
				}
				i++
			}
			continue
		}
		i++
	}
	return string(chars)
}

func extractBracedBlock(text, marker string) (string, error) {
	start := strings.Index(text, marker)
	if start < 0 {
		return "", fmt.Errorf("macro marker not found: %q", marker)
	}
	openIdx := strings.Index(text[start+len(marker)-1:], "{")
	if openIdx < 0 {
		return "", fmt.Errorf("no opening brace after %q", marker)
	}
	openIdx += start + len(marker) - 1
	blanked := blankNoise(text)
	depth := 0
	for i := openIdx; i < len(blanked); i++ {
		if blanked[i] == '{' {
			depth++
		} else if blanked[i] == '}' {
			depth--
			if depth == 0 {
				return text[openIdx+1 : i], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced braces for %q", marker)
}

func camel(name string) string {
	if name == "" {
		return name
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func rustType(body, key string) *string {
	rx := reParams
	if key == "response" {
		rx = reResponse
	}
	m := rx.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	raw := strings.TrimSpace(m[1])
	raw = strings.TrimSpace(reAttrInline.ReplaceAllString(raw, ""))
	if strings.Contains(raw, "Option<()>") || raw == "()" || raw == "" {
		return nil
	}
	raw = strings.TrimRight(raw, ",")
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, "::")
	last := strings.TrimSpace(parts[len(parts)-1])
	found := reWord.FindStringSubmatch(last)
	if found == nil {
		return nil
	}
	name := found[1]
	if name == "Option" || name == "Vec" || attrWords[name] {
		return nil
	}
	return &name
}

func parseSource(commonRs string) ([]methodEntry, error) {
	var methods []methodEntry
	for _, bm := range blockMarkers {
		block, err := extractBracedBlock(commonRs, bm.marker)
		if err != nil {
			return nil, err
		}
		var pendingExp *string
		var pendingWire *string
		var current *methodEntry
		var body []string

		flush := func() {
			if current == nil {
				return
			}
			joined := strings.Join(body, "\n")
			current.ParamsType = rustType(joined, "params")
			current.ResponseType = rustType(joined, "response")
			methods = append(methods, *current)
		}

		for _, rawLine := range strings.Split(block, "\n") {
			s := strings.TrimSpace(rawLine)
			if s == "" || strings.HasPrefix(s, "//") {
				continue
			}
			for {
				am := reAttr.FindStringSubmatch(s)
				if am == nil {
					break
				}
				attrBody, rest := am[1], am[2]
				s = rest
				if em := reExperimental.FindStringSubmatch(attrBody); em != nil {
					v := em[1]
					pendingExp = &v
				}
				for _, rx := range []*regexp.Regexp{reRename, reStrum} {
					if f := rx.FindStringSubmatch(attrBody); f != nil && pendingWire == nil {
						w := f[1]
						pendingWire = &w
					}
				}
				if s == "" {
					break
				}
			}

			var variant, explicitWire string
			matched := false
			hasExplicit := false
			if vm := reVariant.FindStringSubmatch(s); vm != nil {
				variant = vm[1]
				explicitWire = vm[2]
				hasExplicit = true
				matched = true
			} else if vm := reVariantBare.FindStringSubmatch(s); vm != nil {
				variant = vm[1]
				matched = true
			}
			if matched {
				flush()
				method := camel(variant)
				if hasExplicit && explicitWire != "" {
					method = explicitWire
				} else if pendingWire != nil {
					method = *pendingWire
				}
				e := methodEntry{
					Method:             method,
					Face:               bm.face,
					Variant:            variant,
					Experimental:       pendingExp != nil,
					ExperimentalReason: pendingExp,
				}
				current = &e
				body = []string{s}
				pendingExp = nil
				pendingWire = nil
				continue
			}
			if current != nil {
				body = append(body, s)
			}
		}
		flush()
	}
	return methods, nil
}

// --- export parsing ---------------------------------------------------------

func collectMethods(node any, out map[string]bool) {
	switch t := node.(type) {
	case map[string]any:
		if tag, ok := t["method"].(map[string]any); ok {
			if enum, ok := tag["enum"].([]any); ok {
				for _, x := range enum {
					if s, ok := x.(string); ok {
						out[s] = true
					}
				}
			}
			if c, ok := tag["const"].(string); ok {
				out[c] = true
			}
		}
		for _, v := range t {
			collectMethods(v, out)
		}
	case []any:
		for _, v := range t {
			collectMethods(v, out)
		}
	}
}

func schemaFile(face string) string {
	switch face {
	case "client_request":
		return "ClientRequest.json"
	case "server_request":
		return "ServerRequest.json"
	case "server_notification":
		return "ServerNotification.json"
	default:
		return "ClientNotification.json"
	}
}

func zstdDecompress(path string) ([]byte, error) {
	zstd, err := exec.LookPath("zstd")
	if err != nil {
		return nil, fmt.Errorf("reading .zst exports requires the `zstd` command-line tool; `verify` does not need it -- only `sync` does")
	}
	out, err := exec.Command(zstd, "-dc", "--", path).Output()
	if err != nil {
		return nil, fmt.Errorf("zstd -dc %s: %w", path, err)
	}
	return out, nil
}

func loadExportBundle(path string) (map[string]string, error) {
	raw, err := zstdDecompress(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		JSONSchema map[string]json.RawMessage `json:"json_schema"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range doc.JSONSchema {
		// Each value is a JSON *string* holding the schema (the bundle is double-encoded),
		// exactly as Python's json.loads(bundle[file]) expects.
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, nil
}

func exportMethods(bundle map[string]string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, face := range faces {
		var node any
		if err := json.Unmarshal([]byte(bundle[schemaFile(face)]), &node); err != nil {
			return nil, err
		}
		set := map[string]bool{}
		collectMethods(node, set)
		out[face] = set
	}
	return out, nil
}

func loadMethodSetFile(path string) (methodSet, error) {
	var ms methodSet
	b, err := os.ReadFile(path)
	if err != nil {
		return ms, err
	}
	if err := json.Unmarshal(b, &ms); err != nil {
		return ms, err
	}
	return ms, nil
}

func dumpMethodSet(sets map[string]map[string]bool) string {
	ms := methodSet{}
	for _, face := range faces {
		var list []string
		for k := range sets[face] {
			list = append(list, k)
		}
		sort.Strings(list)
		if list == nil {
			list = []string{}
		}
		ms.set(face, list)
	}
	b, _ := json.MarshalIndent(ms, "", "  ")
	return string(b) + "\n"
}

func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func writeBytes(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// --- sync -------------------------------------------------------------------

type surfaceCounts struct {
	ByFace       byFace `json:"by_face"`
	Experimental int    `json:"experimental"`
}
type methodSurfaceDoc struct {
	CodexCommit string        `json:"codex_commit"`
	Source      string        `json:"source"`
	Counts      surfaceCounts `json:"counts"`
	Methods     []methodEntry `json:"methods"`
}

func cmdSync(args []string) int {
	codexSrc, ok := argValue(args, "--codex-src")
	if !ok {
		fmt.Fprintln(os.Stderr, "codexgen sync: --codex-src is required")
		return 2
	}
	repo, _ := filepath.Abs(codexSrc)
	sourcePath := filepath.Join(repo, sourceRel)
	if _, err := os.Stat(sourcePath); err != nil {
		fmt.Fprintf(os.Stderr, "error: codex source not found: %s\n", sourcePath)
		return 2
	}

	commit := "unknown"
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
	}

	methods, err := parseSource(readFile(sourcePath))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	stableBundle, err := loadExportBundle(filepath.Join(repo, precomputedDir, "app-server-exports-stable.json.zst"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	expBundle, err := loadExportBundle(filepath.Join(repo, precomputedDir, "app-server-exports-experimental.json.zst"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	stableMethods, err := exportMethods(stableBundle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	expMethods, err := exportMethods(expBundle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}

	aggSrc := filepath.Join(repo, aggregatedJSON)
	if _, err := os.Stat(aggSrc); err != nil {
		fmt.Fprintf(os.Stderr, "error: aggregated schema not found: %s\n", aggSrc)
		return 2
	}
	if err := writeBytes(filepath.Join(schemaDirPath(), aggregatedSchema), readFileBytes(aggSrc)); err != nil {
		fatal(err)
	}

	if err := os.MkdirAll(exportsDir(), 0o755); err != nil {
		fatal(err)
	}
	writeFile(filepath.Join(exportsDir(), "stable-methods.json"), dumpMethodSet(stableMethods))
	writeFile(filepath.Join(exportsDir(), "experimental-methods.json"), dumpMethodSet(expMethods))

	excluded := map[string]bool{}
	for _, e := range exportExclusions {
		excluded[pair(e.Method, e.Face)] = true
	}
	for i := range methods {
		m := &methods[i]
		m.InExportStable = stableMethods[m.Face][m.Method]
		m.InExportExperimental = expMethods[m.Face][m.Method]
		m.ExportExcluded = excluded[pair(m.Method, m.Face)]
	}

	countsByFace := byFace{}
	countsExp := 0
	for _, face := range faces {
		n := 0
		for _, m := range methods {
			if m.Face == face {
				n++
			}
		}
		countsByFace.set(face, n)
	}
	for _, m := range methods {
		if m.Experimental {
			countsExp++
		}
	}
	surfaceDoc := methodSurfaceDoc{
		CodexCommit: commit,
		Source:      sourceRel,
		Counts:      surfaceCounts{countsByFace, countsExp},
		Methods:     methods,
	}
	sb, _ := json.MarshalIndent(surfaceDoc, "", "  ")
	writeFile(filepath.Join(genDirPath(), "method-surface.json"), string(sb)+"\n")
	eb, _ := json.MarshalIndent(exportExclusions, "", "  ")
	writeFile(filepath.Join(genDirPath(), "export-exclusions.json"), string(eb)+"\n")

	sortedMethods := append([]methodEntry(nil), methods...)
	sort.Slice(sortedMethods, func(i, j int) bool {
		if sortedMethods[i].Face != sortedMethods[j].Face {
			return sortedMethods[i].Face < sortedMethods[j].Face
		}
		return sortedMethods[i].Method < sortedMethods[j].Method
	})
	var ni strings.Builder
	ni.WriteString("# Derived: source methods marked #[experimental] -- out of scope by decision R2.\n")
	ni.WriteString("# Regenerate with: make sync\n")
	for _, m := range sortedMethods {
		if m.Experimental {
			fmt.Fprintf(&ni, "%-20s %s\n", m.Face, m.Method)
		}
	}
	writeFile(filepath.Join(genDirPath(), "not-in-scope.txt"), ni.String())

	declaredStable := byFace{}
	sourceExperimental := byFace{}
	for _, face := range faces {
		d, e := 0, 0
		for _, m := range methods {
			if m.Face != face {
				continue
			}
			if m.Experimental {
				e++
			} else {
				d++
			}
		}
		declaredStable.set(face, d)
		sourceExperimental.set(face, e)
	}
	schemaRev, err := sha256File(filepath.Join(schemaDirPath(), aggregatedSchema))
	if err != nil {
		fatal(err)
	}
	versionGo := renderVersionGo(commit, schemaRev, declaredStable, sourceExperimental, countsExp)
	writeFile(filepath.Join(schemaDirPath(), "version.go"), versionGo)

	artifacts := newOrderedMap()
	for _, p := range []string{
		filepath.Join(schemaDirPath(), aggregatedSchema),
		filepath.Join(schemaDirPath(), "version.go"),
		filepath.Join(exportsDir(), "stable-methods.json"),
		filepath.Join(exportsDir(), "experimental-methods.json"),
		filepath.Join(genDirPath(), "method-surface.json"),
		filepath.Join(genDirPath(), "export-exclusions.json"),
	} {
		h, err := sha256File(p)
		if err != nil {
			fatal(err)
		}
		rel, _ := filepath.Rel(root(), p)
		artifacts.set(filepath.ToSlash(rel), h)
	}
	exportStableCounts := byFace{}
	exportExpCounts := byFace{}
	for _, face := range faces {
		exportStableCounts.set(face, len(stableMethods[face]))
		exportExpCounts.set(face, len(expMethods[face]))
	}
	manifest := manifestDoc{
		CodexCommit: commit,
		GeneratedBy: "tools/codexgen sync",
		ScopeModel:  "declared_stable = source non-experimental; exports used for reconciliation",
		Artifacts:   artifacts,
		Measured: manifestMeasured{
			Source:             countsByFace,
			SourceExperimental: sourceExperimental,
			ExportStable:       exportStableCounts,
			ExportExperimental: exportExpCounts,
		},
		DeclaredStable: declaredStable,
	}
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	writeFile(filepath.Join(vendorDir(), "manifest.json"), string(mb)+"\n")

	if len(unionAll(stableMethods)) == 0 && len(unionAll(expMethods)) == 0 {
		fmt.Fprintln(os.Stderr, "error: exports parsed empty; refusing to write artifacts")
		return 2
	}

	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	fmt.Printf("sync: codex %s\n", short)
	for _, face := range faces {
		fmt.Printf("  %-20s source=%3d exp=%3d declared_stable=%3d export_stable=%3d export_exp=%3d\n",
			face, countsByFace.get(face), sourceExperimental.get(face), declaredStable.get(face),
			exportStableCounts.get(face), exportExpCounts.get(face))
	}
	fmt.Printf("  vendored -> %s, exports/*, gen/*\n", aggregatedSchema)
	return 0
}

func (b byFace) get(face string) int {
	switch face {
	case "client_request":
		return b.ClientRequest
	case "server_request":
		return b.ServerRequest
	case "server_notification":
		return b.ServerNotification
	default:
		return b.ClientNotification
	}
}

func unionAll(m map[string]map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range m {
		for k := range s {
			out[k] = true
		}
	}
	return out
}

type manifestMeasured struct {
	Source             byFace `json:"source"`
	SourceExperimental byFace `json:"source_experimental"`
	ExportStable       byFace `json:"export_stable"`
	ExportExperimental byFace `json:"export_experimental"`
}
type manifestDoc struct {
	CodexCommit    string           `json:"codex_commit"`
	GeneratedBy    string           `json:"generated_by"`
	ScopeModel     string           `json:"scope_model"`
	Artifacts      *orderedMap      `json:"artifacts"`
	Measured       manifestMeasured `json:"measured"`
	DeclaredStable byFace           `json:"declared_stable"`
}

func readFileBytes(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	return b
}

func renderVersionGo(commit, schemaRev string, ds, se byFace, notInScope int) string {
	return fmt.Sprintf(`package schema

// Code generated by tools/codexgen sync. DO NOT EDIT.
//
// Upstream Codex publishes no protocol version number (codex-rs/Cargo.toml is uniformly
// "0.0.0" and JSONRPC_VERSION in src/rpc.rs is dead code), so the anchor is the codex git
// commit plus the hashes of the vendored artifacts. The machine-readable surface lives in
// .codex-schema/manifest.json and gen/method-surface.json.
//
// Scope model: declared_stable == source methods WITHOUT #[experimental(...)]. The
// precomputed exports are used only for reconciliation -- in particular the export does
// NOT filter experimental ServerNotifications, so notifications can never be scoped by
// the export alone.

const (
	// SourceCodexCommit is the github.com/openai/codex commit all artifacts came from.
	SourceCodexCommit = "%s"

	// SchemaRevision is the sha256 of the vendored %s
	// (the upstream *stable* aggregate; it excludes the 5 methods listed in
	// gen/export-exclusions.json).
	SchemaRevision = "%s"

	// SchemaTitle mirrors the upstream aggregate's title for traceability.
	SchemaTitle = "Codex app-server protocol v2 (stable)"

	// DeclaredStable counts source methods WITHOUT an #[experimental] annotation, per face.
	DeclaredStableClientRequest       = %d
	DeclaredStableServerRequest       = %d
	DeclaredStableServerNotification  = %d
	DeclaredStableClientNotification  = %d

	// SourceExperimental counts methods annotated #[experimental], per face. These are
	// out of scope by decision R2 (see gen/not-in-scope.txt).
	SourceExperimentalClientRequest      = %d
	SourceExperimentalServerRequest      = %d
	SourceExperimentalServerNotification = %d
	SourceExperimentalClientNotification = %d

	// NotInScopeTotal is DeclaredStable's complement: every #[experimental] method.
	NotInScopeTotal = %d
)
`, commit, aggregatedSchema, schemaRev,
		ds.ClientRequest, ds.ServerRequest, ds.ServerNotification, ds.ClientNotification,
		se.ClientRequest, se.ServerRequest, se.ServerNotification, se.ClientNotification,
		notInScope)
}

// --- verify -----------------------------------------------------------------

func cmdVerify(args []string) int {
	var surface methodSurfaceDoc
	b, err := os.ReadFile(filepath.Join(genDirPath(), "method-surface.json"))
	if err != nil {
		fatal(err)
	}
	if err := json.Unmarshal(b, &surface); err != nil {
		fatal(err)
	}
	stable, err := loadMethodSetFile(filepath.Join(exportsDir(), "stable-methods.json"))
	if err != nil {
		fatal(err)
	}
	exp, err := loadMethodSetFile(filepath.Join(exportsDir(), "experimental-methods.json"))
	if err != nil {
		fatal(err)
	}
	var exclusions []exportExclusion
	loadJSON(filepath.Join(genDirPath(), "export-exclusions.json"), &exclusions)

	allSrc := map[string]bool{}
	for _, m := range surface.Methods {
		allSrc[pair(m.Method, m.Face)] = true
	}
	expAll := map[string]bool{}
	for _, face := range faces {
		for _, m := range exp.get(face) {
			expAll[pair(m, face)] = true
		}
	}
	excl := map[string]bool{}
	for _, e := range exclusions {
		excl[pair(e.Method, e.Face)] = true
	}

	ok := true
	pinned := surface.CodexCommit
	if pinned == "" {
		pinned = "?"
	}
	short := pinned
	if len(short) > 12 {
		short = short[:12]
	}
	fmt.Printf("verify: codex %s\n", short)
	ok = reportOK(ok, reportSurfaceFresh(pinned))

	diff := diffBool(allSrc, expAll)
	ok = reportOK(ok, report(diffEqual(diff, excl), "source - export_experimental == export-exclusions",
		fmt.Sprintf("n=%d", len(diff))+func() string {
			if diffEqual(diff, excl) {
				return ""
			}
			return fmt.Sprintf("; unexpected=%s; missing=%s", pyStringList(sortedBoolKeys(diffBool(diff, excl))), pyStringList(sortedBoolKeys(diffBool(excl, diff))))
		}()))

	extra := diffBool(expAll, allSrc)
	detail := ""
	if len(extra) > 0 {
		detail = fmt.Sprintf("extra=%s", pyStringList(sortedBoolKeys(extra)))
	}
	ok = reportOK(ok, report(len(extra) == 0, "export_experimental - source == {} ", detail))

	for _, face := range []string{"client_request", "server_request"} {
		derived := diffSlices(exp.get(face), stable.get(face))
		expected := map[string]bool{}
		for _, m := range surface.Methods {
			if m.Face == face && m.Experimental {
				expected[m.Method] = true
			}
		}
		detail := fmt.Sprintf("derived=%d expected=%d", len(derived), len(expected))
		if !strSetEqual(derived, expected) {
			detail += fmt.Sprintf("; diff=%s", pyStringList(sortedBoolKeys(symDiff(derived, expected))))
		}
		ok = reportOK(ok, report(strSetEqual(derived, expected),
			fmt.Sprintf("%s: export_exp - export_stable == source experimental", face), detail))
	}

	ok = reportOK(ok, report(strSliceSetEqual(stable.get("server_notification"), exp.get("server_notification")),
		"server_notification: export_stable == export_experimental (known: notifications are not filtered)",
		fmt.Sprintf("stable=%d exp=%d", len(stable.get("server_notification")), len(exp.get("server_notification")))))

	declared := map[string]bool{}
	for _, m := range surface.Methods {
		if m.Face == "client_request" && !m.Experimental {
			declared[m.Method] = true
		}
	}
	exclCR := map[string]bool{}
	for _, e := range exclusions {
		if e.Face == "client_request" {
			exclCR[e.Method] = true
		}
	}
	ok = reportOK(ok, report(strSetEqual(toSet(stable.get("client_request")), diffBool(declared, exclCR)),
		"client_request: export_stable == declared_stable - exclusions",
		fmt.Sprintf("export=%d declared=%d excl=%d", len(stable.get("client_request")), len(declared), len(exclCR))))

	declaredN := map[string]bool{}
	leaked := map[string]bool{}
	for _, m := range surface.Methods {
		if m.Face != "server_notification" {
			continue
		}
		if m.Experimental {
			leaked[m.Method] = true
		} else {
			declaredN[m.Method] = true
		}
	}
	internal := map[string]bool{}
	for _, e := range exclusions {
		if e.Face == "server_notification" {
			internal[e.Method] = true
		}
	}
	ok = reportOK(ok, report(strSetEqual(toSet(stable.get("server_notification")), diffBool(unionSets(declaredN, leaked), internal)),
		"server_notification: export_stable == declared_stable + leaked - internal",
		fmt.Sprintf("export=%d declared=%d leaked=%d internal=%d", len(stable.get("server_notification")), len(declaredN), len(leaked), len(internal))))

	fmt.Println()
	if ok {
		fmt.Println("verify: all assertions passed")
		return 0
	}
	fmt.Fprintln(os.Stderr, "verify: FAILED — re-run `make sync` against the pinned codex commit, then review")
	return 1
}

func reportSurfaceFresh(pinned string) bool {
	src := os.Getenv("CODEX_SRC")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, "github.com", "openai", "codex")
	}
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		fmt.Printf("  [SKIP] surface freshness: no codex checkout at %s (set CODEX_SRC to check)\n", src)
		return true
	}
	out, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		fmt.Printf("  [SKIP] surface freshness: cannot read HEAD of %s (%v)\n", src, err)
		return true
	}
	head := strings.TrimSpace(string(out))
	short := func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	}
	if head == pinned {
		fmt.Printf("  [PASS] surface is from the checkout's commit (%s)\n", short(pinned))
		return true
	}
	fmt.Println("  [FAIL] the vendored surface is from a DIFFERENT commit than your checkout")
	fmt.Printf("         surface: %s\n", short(pinned))
	fmt.Printf("         checkout: %s\n", short(head))
	fmt.Println("         Every coverage result below is measured against the surface, not your")
	fmt.Println("         checkout, so a clean result here does not mean the SDK matches the source")
	fmt.Printf("         in front of you. Run `make sync` against %s.\n", src)
	return false
}

// --- diff-cli ---------------------------------------------------------------

func cmdDiffCLI(args []string) int {
	bundleArg, ok := argValue(args, "--bundle")
	if !ok {
		fmt.Fprintln(os.Stderr, "codexgen diff-cli: --bundle is required")
		return 2
	}
	bundle, _ := filepath.Abs(bundleArg)
	if _, err := os.Stat(filepath.Join(bundle, "ClientRequest.json")); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s/ClientRequest.json not found; pass a generate-json-schema --out dir\n", bundle)
		return 2
	}
	cli := map[string]map[string]bool{}
	for _, face := range faces {
		var node any
		if err := json.Unmarshal(readFileBytes(filepath.Join(bundle, schemaFile(face))), &node); err != nil {
			fatal(err)
		}
		set := map[string]bool{}
		collectMethods(node, set)
		cli[face] = set
	}
	stable, err := loadMethodSetFile(filepath.Join(exportsDir(), "stable-methods.json"))
	if err != nil {
		fatal(err)
	}

	fmt.Printf("diff-cli: %s\n", bundle)
	drifted := false
	for _, face := range faces {
		st := toSet(stable.get(face))
		missing := diffBool(st, cli[face])
		extra := diffBool(cli[face], st)
		status := "OK"
		if len(missing) > 0 || len(extra) > 0 {
			status = "DRIFT"
		}
		drifted = drifted || len(missing) > 0 || len(extra) > 0
		fmt.Printf("  [%s] %-20s cli=%3d anchor_stable=%3d\n", status, face, len(cli[face]), len(st))
		if len(missing) > 0 {
			fmt.Printf("         the CLI is BEHIND the anchor; missing %d: %s\n", len(missing), pyStringList(firstN(sortedBoolKeys(missing), 8)))
		}
		if len(extra) > 0 {
			fmt.Printf("         the CLI is AHEAD of the anchor; extra %d: %s\n", len(extra), pyStringList(firstN(sortedBoolKeys(extra), 8)))
		}
	}
	if drifted {
		fmt.Fprintln(os.Stderr, "\ndiff-cli: the installed CLI does not match the pinned commit.")
		fmt.Fprintln(os.Stderr, "  The anchor is the pinned commit, NOT the installed CLI. Regenerate the CLI or")
		fmt.Fprintln(os.Stderr, "  re-pin deliberately (and re-run every review in the plan) before using its output.")
		return 1
	}
	fmt.Println("\ndiff-cli: installed CLI matches the pinned anchor")
	return 0
}

// --- set helpers ------------------------------------------------------------

func report(ok bool, label, detail string) bool {
	mark := "FAIL"
	if ok {
		mark = "PASS"
	}
	if detail != "" {
		fmt.Printf("  [%s] %s — %s\n", mark, label, detail)
	} else {
		fmt.Printf("  [%s] %s\n", mark, label)
	}
	return ok
}
func reportOK(a, b bool) bool { return a && b }

func diffBool(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if !b[k] {
			out[k] = true
		}
	}
	return out
}
func diffEqual(a, b map[string]bool) bool {
	return len(diffBool(a, b)) == 0 && len(diffBool(b, a)) == 0
}
func diffStrSet(a map[string]bool, b []string) map[string]bool {
	bs := map[string]bool{}
	for _, x := range b {
		bs[x] = true
	}
	return diffBool(a, bs)
}

// diffSlices returns toSet(a) - toSet(b).
func diffSlices(a, b []string) map[string]bool { return diffBool(toSet(a), toSet(b)) }
func strSetEqual(a, b map[string]bool) bool {
	return len(diffBool(a, b)) == 0 && len(diffBool(b, a)) == 0
}
func strSliceSetEqual(a []string, b []string) bool {
	return strSetEqual(toSet(a), toSet(b))
}
func toSet(items []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range items {
		out[s] = true
	}
	return out
}
func unionSets(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}
func symDiff(a, b map[string]bool) map[string]bool {
	return unionSets(diffBool(a, b), diffBool(b, a))
}
func sortedBoolKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
