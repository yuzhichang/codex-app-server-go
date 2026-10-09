package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Port of scripts/gen_readme_coverage.py: regenerates the block between the
// coverage:start / coverage:end markers in README.md from gen/method-surface.json and
// gen/implemented-methods.json.

const (
	covStart = "<!-- coverage:start -->"
	covEnd   = "<!-- coverage:end -->"
)

type subsystem struct {
	name     string
	prefixes []string
}

var subsystems = []subsystem{
	{"Core / Initialize", []string{"initialize", "initialized", "serverRequest/"}},
	{"Thread", []string{"thread/"}},
	{"Thread sections", []string{"threadSection/"}},
	{"Turn", []string{"turn/"}},
	{"Account / Login", []string{"account/"}},
	{"Models", []string{"model/", "modelProvider/"}},
	{"Review", []string{"review/"}},
	{"Config", []string{"config/", "configRequirements/"}},
	{"Skills", []string{"skills/"}},
	{"Plugins / Marketplace", []string{"plugin/", "marketplace/"}},
	{"Apps", []string{"app/"}},
	{"Filesystem", []string{"fs/"}},
	{"MCP", []string{"mcpServer", "config/mcpServer"}},
	{"Command exec", []string{"command/"}},
	{"Experimental features", []string{"experimentalFeature/"}},
	{"Hooks", []string{"hooks/"}},
	{"External agent config", []string{"externalAgentConfig/"}},
	{"Permissions", []string{"permissionProfile/"}},
	{"Windows sandbox", []string{"windowsSandbox/"}},
}

func subsystemOf(method string) string {
	for _, s := range subsystems {
		for _, p := range s.prefixes {
			if strings.HasPrefix(method, p) {
				return s.name
			}
		}
	}
	return "Other"
}

var commitRe = regexp.MustCompile(`SourceCodexCommit\s*=\s*"([^"]+)"`)

func pinnedCommit() string {
	text := readFile(filepath.Join(schemaDir(), "version.go"))
	if m := commitRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return "unknown"
}

func readmeCoverageBlock() string {
	var surface surfaceDoc
	loadJSON(filepath.Join(genDir(), "method-surface.json"), &surface)
	var impl []implEntry
	loadJSON(filepath.Join(genDir(), "implemented-methods.json"), &impl)
	var wl []whitelistEntry
	loadJSON(filepath.Join(genDir(), "whitelist.json"), &wl)

	implemented := map[string]struct{}{}
	for _, e := range impl {
		implemented[pair(e.Method, e.Face)] = struct{}{}
	}
	whitelist := map[string]struct{}{}
	for _, w := range wl {
		whitelist[pair(w.Method, w.Face)] = struct{}{}
	}

	var declared []surfaceMethod
	for _, m := range surface.Methods {
		if !m.Experimental {
			declared = append(declared, m)
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Aligned to codex commit `%s`; scope = **stable** only.", pinnedCommit()))
	lines = append(lines, "")
	lines = append(lines, "Every number below is generated from `gen/method-surface.json` and "+
		"`gen/implemented-methods.json`, and enforced by `make conformance-strict`.")
	lines = append(lines, "")

	var requests []surfaceMethod
	for _, m := range declared {
		if m.Face == "client_request" {
			requests = append(requests, m)
		}
	}
	grouped := map[string][]surfaceMethod{}
	for _, m := range requests {
		s := subsystemOf(m.Method)
		grouped[s] = append(grouped[s], m)
	}

	lines = append(lines, "### Client → server requests")
	lines = append(lines, "")
	lines = append(lines, "| Subsystem | Implemented | Declared | Methods |")
	lines = append(lines, "|---|:---:|:---:|---|")
	for _, s := range subsystems {
		members := grouped[s.name]
		if len(members) == 0 {
			continue
		}
		done, skipped := 0, 0
		for _, m := range members {
			if _, ok := implemented[pair(m.Method, m.Face)]; ok {
				done++
			}
			if _, ok := whitelist[pair(m.Method, m.Face)]; ok {
				skipped++
			}
		}
		sortedMembers := append([]surfaceMethod(nil), members...)
		sort.Slice(sortedMembers, func(i, j int) bool { return sortedMembers[i].Method < sortedMembers[j].Method })
		var names []string
		for _, m := range sortedMembers {
			short := m.Method
			for _, prefix := range []string{"thread/", "turn/", "account/", "plugin/", "app/", "fs/"} {
				if strings.HasPrefix(short, prefix) {
					short = short[len(prefix):]
					break
				}
			}
			if _, ok := whitelist[pair(m.Method, m.Face)]; ok {
				names = append(names, fmt.Sprintf("~~`%s`~~", short))
			} else {
				names = append(names, fmt.Sprintf("`%s`", short))
			}
		}
		lines = append(lines, fmt.Sprintf("| %s | %d | %d | %s |", s.name, done, len(members)-skipped, strings.Join(names, " · ")))
	}

	known := map[string]bool{}
	for _, s := range subsystems {
		known[s.name] = true
	}
	var extras []string
	for k := range grouped {
		if !known[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		members := grouped[key]
		done := 0
		for _, m := range members {
			if _, ok := implemented[pair(m.Method, m.Face)]; ok {
				done++
			}
		}
		sortedMembers := append([]surfaceMethod(nil), members...)
		sort.Slice(sortedMembers, func(i, j int) bool { return sortedMembers[i].Method < sortedMembers[j].Method })
		names := make([]string, len(sortedMembers))
		for i, m := range sortedMembers {
			names[i] = fmt.Sprintf("`%s`", m.Method)
		}
		lines = append(lines, fmt.Sprintf("| %s | %d | %d | %s |", key, done, len(members), strings.Join(names, " · ")))
	}

	lines = append(lines, "")
	lines = append(lines, "### Server → client")

	countFace := func(face string) (total, done int) {
		for _, m := range declared {
			if m.Face != face {
				continue
			}
			total++
			if _, ok := implemented[pair(m.Method, m.Face)]; ok {
				done++
			}
		}
		return total, done
	}
	notesTotal, notesDone := countFace("server_notification")
	reqsTotal, reqsDone := countFace("server_request")

	lines = append(lines, "")
	lines = append(lines, "| Kind | Implemented | Declared |")
	lines = append(lines, "|---|:---:|:---:|")
	lines = append(lines, fmt.Sprintf("| Notifications (typed decoders) | %d | %d |", notesDone, notesTotal))
	lines = append(lines, fmt.Sprintf("| Server-initiated requests (handlers) | %d | %d |", reqsDone, reqsTotal))
	lines = append(lines, "")

	totalDone, totalSkipped := 0, 0
	for _, m := range declared {
		if _, ok := implemented[pair(m.Method, m.Face)]; ok {
			totalDone++
		}
		if _, ok := whitelist[pair(m.Method, m.Face)]; ok {
			totalSkipped++
		}
	}
	lines = append(lines, fmt.Sprintf("**Total: %d of %d in-scope methods implemented** "+
		"(%d deliberately not implemented, struck through above and listed in "+
		"`gen/not-implemented.txt`, with a reason for each in `gen/whitelist.json`).",
		totalDone, len(declared)-totalSkipped, totalSkipped))
	lines = append(lines, "")
	lines = append(lines, "Methods upstream marks `#[experimental]` are out of scope by decision R2 and are "+
		"listed in `gen/not-in-scope.txt`.")
	return strings.Join(lines, "\n")
}

func cmdReadmeCoverage(args []string) int {
	check := hasFlag(args, "--check")
	block := readmeCoverageBlock()
	readmePath := filepath.Join(root(), "README.md")
	text := readFile(readmePath)

	si := strings.Index(text, covStart)
	ei := strings.Index(text, covEnd)
	if si < 0 || ei < 0 {
		fatal(fmt.Errorf("README.md is missing the %s / %s markers", covStart, covEnd))
	}
	head := text[:si]
	tail := text[ei+len(covEnd):]
	updated := head + covStart + "\n\n" + block + "\n" + covEnd + tail

	if check {
		if updated != text {
			fmt.Println("README.md coverage block is out of date; run `make readme-coverage`")
			return 1
		}
		fmt.Println("README.md coverage block is up to date")
		return 0
	}
	writeFile(readmePath, updated)
	fmt.Println("README.md coverage block regenerated")
	return 0
}
