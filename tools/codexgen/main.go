// Command codexgen is the single Go entry point for the SDK's generation and
// conformance tooling. It replaces the former Python scripts one subcommand at a time;
// see the Makefile for how each is wired.
//
// Run from the module root:
//
//	go run ./tools/codexgen <command> [args]
//
// Commands:
//
//	readme-coverage [--check]         regenerate the README coverage block
//	coverage report|write|check [--strict]
//	typecheck report|check
//	exportcheck report|check
//	enumvalue report|check
//	typeshape report|check            (needs the generator; go/typeshape)
//	gen-types ... | gen-client ...    (codegen)
//	sync | verify | diff-cli          (schema surface)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// root is the module root; every command runs from there (the Makefile cds first).
func root() string { return "." }

func genDir() string    { return filepath.Join(root(), "gen") }
func schemaDir() string { return filepath.Join(root(), "internal", "protocol", "schema") }
func protocolDir() string {
	return filepath.Join(root(), "internal", "protocol")
}
func aggregatePath() string {
	return filepath.Join(schemaDir(), "codex_app_server_protocol.v2.schemas.json")
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var code int
	switch cmd {
	case "readme-coverage":
		code = cmdReadmeCoverage(args)
	case "coverage":
		code = cmdCoverage(args)
	case "typecheck":
		code = cmdTypeCheck(args)
	case "exportcheck":
		code = cmdExportCheck(args)
	case "enumvalue":
		code = cmdEnumValue(args)
	default:
		fmt.Fprintf(os.Stderr, "codexgen: unknown command %q\n", cmd)
		usage()
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: codexgen <command> [args]")
	fmt.Fprintln(os.Stderr, "commands: readme-coverage coverage typecheck exportcheck enumvalue")
	os.Exit(2)
}

// --- shared helpers ---------------------------------------------------------

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	return string(b)
}

func loadJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		fatal(fmt.Errorf("%s: %w", path, err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "codexgen:", err)
	os.Exit(2)
}

// mode parses a leading optional "report"/"check" argument, defaulting to "report"
// (mirrors argparse's nargs="?" default).
func mode(args []string, allowed ...string) string {
	if len(args) == 0 {
		if len(allowed) > 0 {
			return allowed[0]
		}
		return "report"
	}
	for _, a := range allowed {
		if args[0] == a {
			return a
		}
	}
	fmt.Fprintf(os.Stderr, "codexgen: invalid mode %q (want one of %v)\n", args[0], allowed)
	os.Exit(2)
	return ""
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}
