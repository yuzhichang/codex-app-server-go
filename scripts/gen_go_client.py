#!/usr/bin/env python3
"""Generate typed Client methods (and their tests) from the extracted surface (plan T1.2).

Why generate these
------------------
The remaining stable client requests are mechanical bindings:
`Call(ctx, Const, req, &resp)`. Hand-writing them adds no judgement but adds dozens of
chances to typo a wire string -- and the coverage gate can only prove a *constant* is
referenced, not that the string behind it is right. Deriving the method, its type aliases
and its test from `gen/method-surface.json` removes the transcription step.

A method is skipped when the package already binds that constant to a client method, so
hand-written methods always win.

  python3 scripts/gen_go_client.py [--dry-run]
"""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
SDK_ROOT = SCRIPT_DIR.parent
GEN_DIR = SDK_ROOT / "gen"
ENVELOPE = SDK_ROOT / "internal" / "protocol" / "envelope.go"
GEN_CONSTS = SDK_ROOT / "internal" / "protocol" / "generated_methods.go"
GEN_METHODS = SDK_ROOT / "generated_client_methods.go"
GEN_TESTS = SDK_ROOT / "generated_client_methods_test.go"

_CONST_DECL = re.compile(r'^\s*(Method\w+)\s*=\s*"([^"]+)"', re.M)
_TYPE_DECL = re.compile(r"^type (\w+)\b", re.M)
# Aliases declared inside a grouped `type ( ... )` block, e.g. types.go. Without this the
# root package looks like it has almost no types and nearly every method is skipped.
_TYPE_ALIAS = re.compile(r"^\t(\w+)\s*=", re.M)


def declared_types(text: str) -> set[str]:
    return set(_TYPE_DECL.findall(text)) | set(_TYPE_ALIAS.findall(text))


# A hand-written client method: name, parameter list and result list.
_CLIENT_SIG = re.compile(r"func \(c \*Client\) (\w+)\(([^)]*)\)\s*([^{]*)\{", re.S)

# Parameter types that can be produced with a literal, so a test can call the method.
_LITERAL_ARGS = {"string": '""', "int": "0", "int64": "0", "bool": "false"}


def _second_param_type(params: str) -> str | None:
    """Return the type of the first non-context parameter, or None if there is none."""
    parts = [p.strip() for p in params.split(",") if p.strip()]
    for part in parts:
        if part.startswith("ctx "):
            continue
        fields = part.split()
        if len(fields) >= 2:
            return fields[-1]
        return part  # unnamed single type, e.g. `func (c *Client) X(ctx context.Context, string)`
    return None
# A client method, from its signature up to the next top-level declaration.
_CLIENT_FUNC = re.compile(r"func \(c \*Client\) (\w+)\(.*?(?=\nfunc |\n// |\Z)", re.S)
_ANY_CLIENT_FUNC = re.compile(r"func \(c \*Client\) (\w+)\(")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    methods = json.loads((GEN_DIR / "method-surface.json").read_text())["methods"]
    whitelist = {(w["method"], w["face"]) for w in json.loads((GEN_DIR / "whitelist.json").read_text())}
    # Include anything a previous run generated, or the constants would be re-emitted
    # (and the alias set recomputed) on every invocation, breaking idempotency.
    const_text = ENVELOPE.read_text() + (GEN_CONSTS.read_text() if GEN_CONSTS.is_file() else "")
    consts = {wire: name for name, wire in _CONST_DECL.findall(const_text)}

    # Hand-written root sources only. The generator's own output is deliberately excluded:
    # counting it would make the second run see its own aliases and methods as pre-existing
    # and emit a different (smaller) file.
    root_text = ""
    root_methods: set[str] = set()
    for path in SDK_ROOT.glob("*.go"):
        if path.name.endswith("_test.go") or path.resolve() in {GEN_METHODS.resolve(), GEN_TESTS.resolve()}:
            continue
        text = path.read_text(errors="replace")
        root_text += text
        root_methods.update(_ANY_CLIENT_FUNC.findall(text))

    # Constant -> the hand-written Go method that binds it. Those methods are never
    # regenerated, but they still need a test that exercises the wire string.
    bound: dict[str, str] = {}
    for match in _CLIENT_FUNC.finditer(root_text):
        body = match.group(0)
        for const in re.findall(r"protocol\.(Method\w+)", body):
            bound.setdefault(const, match.group(1))

    # Types already addressable as `codexgo.X` from *hand-written* sources.
    #
    # Deliberately excludes this generator's own output: reading it back made run two
    # believe its aliases were pre-existing, so it rewrote the file without them and the
    # referenced types vanished (the file erased its own declarations).
    root_types = declared_types(root_text)

    # Signature of each hand-written client method, so a bound method's test can pass the
    # type that method actually takes -- which is not always the upstream params type.
    # arg type + whether the method returns more than just an error.
    signatures: dict[str, tuple[str | None, bool]] = {}
    for match in _CLIENT_SIG.finditer(root_text):
        results = match.group(3).strip()
        signatures[match.group(1)] = (_second_param_type(match.group(2)), "," in results)

    schema_types = set()
    for path in (SDK_ROOT / "internal" / "protocol" / "schema").glob("*.go"):
        if not path.name.endswith("_test.go"):
            schema_types.update(declared_types(path.read_text(errors="replace")))

    new_consts: list[tuple[str, str]] = []
    methods_src: list[str] = []
    aliases: dict[str, str] = {}
    tests: list[str] = []
    skipped: list[str] = []

    for m in methods:
        if m["face"] != "client_request" or m.get("experimental"):
            continue
        if (m["method"], m["face"]) in whitelist:
            continue

        const = consts.get(m["method"])
        if const is None:
            const = "Method" + m["variant"]
            new_consts.append((const, m["method"]))
        if const in bound:
            # Already bound by a hand-written method: never regenerate the method, but do
            # emit a test that exercises it. "Wired but untested" is not coverage, and the
            # test is the only thing pinning the wire string.
            gname = bound[const]
            sig = signatures.get(gname)
            if sig is None:
                skipped.append(f"{m['method']}: bound as {gname} but its signature was not found")
                continue
            arg, multi = sig
            if arg is not None and arg not in root_types and arg not in _LITERAL_ARGS and not arg.startswith("*"):
                skipped.append(
                    f"{m['method']}: bound as {gname} but its parameter type ({arg}) "
                    f"cannot be constructed here"
                )
                continue
            tests.append(_render_test_with_arg(gname, arg, multi, m["method"]))
            continue

        name = m["variant"]
        if name in root_methods:
            skipped.append(f"{m['method']}: Go name {name} already taken")
            continue

        params, resp = m.get("params_type"), m.get("response_type")
        for t in (params, resp):
            if t and t not in schema_types:
                skipped.append(f"{m['method']}: type {t} not present in the vendored schema")
                break
            if t and t not in root_types:
                aliases[t] = t
        else:
            methods_src.append(_render_method(name, const, params, resp, m))
            tests.append(_render_case(name, const, params, m["method"]))

    if args.dry_run:
        print(f"would generate: {len(methods_src)} methods, {len(tests)} tests, "
              f"{len(new_consts)} constants, {len(aliases)} aliases")
        for line in skipped:
            print("  SKIP", line)
        return 0

    _write_consts(new_consts)
    _write_methods(aliases, methods_src)
    _write_tests(tests)

    print(f"generate-client: {len(methods_src)} methods, {len(tests)} tests, "
          f"{len(new_consts)} constants, {len(aliases)} aliases")
    for line in skipped:
        print("  SKIP", line)
    return 0


def _extra_methods_text() -> str:
    """Methods emitted by a previous run, so re-running stays idempotent."""
    files = [GEN_METHODS, SDK_ROOT / "fs.go", SDK_ROOT / "mcp.go", SDK_ROOT / "app.go", SDK_ROOT / "plugin.go"]
    return "".join(f.read_text(errors="replace") for f in files if f.is_file())


def _write_consts(new_consts: list[tuple[str, str]]) -> None:
    # Merge with what is already there. Overwriting would erase every constant as soon as a
    # re-run found them in const_map (they are read back from this very file), leaving the
    # generated methods referring to undefined identifiers.
    # _CONST_DECL yields (name, wire) pairs, so this is already {name: wire}.
    existing = dict(_CONST_DECL.findall(GEN_CONSTS.read_text())) if GEN_CONSTS.is_file() else {}
    merged = {**{const: wire for const, wire in new_consts}, **existing}
    lines = [
        "// Code generated by scripts/gen_go_client.py. DO NOT EDIT.",
        "//",
        "// Method constants for stable client requests that envelope.go did not yet declare.",
        "// Regenerate with `make generate-client`.",
        "",
        "package protocol",
        "",
        "const (",
    ]
    for const in sorted(merged):
        lines.append(f'\t{const} = "{merged[const]}"')
    lines.append(")")
    GEN_CONSTS.write_text("\n".join(lines) + "\n")


def _write_methods(aliases: dict[str, str], methods_src: list[str]) -> None:
    lines = [
        "// Code generated by scripts/gen_go_client.py from gen/method-surface.json. DO NOT EDIT.",
        "//",
        "// Typed bindings for stable client requests that had no hand-written method. Method",
        "// and type names come from the upstream Rust variant/type names. Regenerate with",
        "// `make generate-client`.",
        "",
        "package codexgo",
        "",
    ]
    # Emit imports only for what is actually used: a run that generates nothing must still
    # produce a compiling file rather than one with dangling imports.
    imports: list[str] = []
    if methods_src:
        imports += ['\t"context"', "", '\t"github.com/zealbase/codex-app-server-go/internal/protocol"']
    if aliases:
        if imports:
            imports.append('\tschematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"')
        else:
            imports.append('\tschematypes "github.com/zealbase/codex-app-server-go/internal/protocol/schema"')
    if imports:
        lines.append("import (")
        lines.extend(imports)
        lines.append(")")
        lines.append("")
    if aliases:
        lines.append("// Type aliases for params/response types the root package did not yet expose.")
        lines.append("type (")
        for name in sorted(aliases):
            lines.append(f"\t{name} = schematypes.{name}")
        lines.append(")")
        lines.append("")
    lines.extend(methods_src)
    GEN_METHODS.write_text("\n".join(lines))


def _write_tests(tests: list[str]) -> None:
    lines = [
        "// Code generated by scripts/gen_go_client.py from gen/method-surface.json. DO NOT EDIT.",
        "//",
        "// Every generated binding is exercised against the mock server. The assertion is that",
        "// the *wire method string* is the upstream one -- the one thing the coverage gate",
        "// cannot verify on its own.",
        "",
        "package codexgo_test",
        "",
        "import (",
        '\t"context"',
        '\t"encoding/json"',
        '\t"testing"',
        "",
        '\tcodexgo "github.com/zealbase/codex-app-server-go"',
        ")",
        "",
        "func TestGeneratedClientMethodsUseUpstreamWireMethods(t *testing.T) {",
        "\tcases := []struct {",
        "\t\tname   string",
        "\t\twire   string",
        "\t\tinvoke func(context.Context, *codexgo.Client) error",
        "\t}{",
    ]
    lines.extend(tests)
    lines.extend([
        "\t}",
        "",
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
        '\t\t\t\tt.Fatalf("%s: %v", tc.wire, err)',
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
    ])
    GEN_TESTS.write_text("\n".join(lines))


def _render_method(name: str, const: str, params: str | None, resp: str | None, m: dict) -> str:
    doc = [
        f"// {name} calls `{m['method']}`.",
        "//",
        "// Generated binding. See gen/method-surface.json for the authoritative surface.",
    ]
    arg = f"req {params}" if params else ""
    argpart = f", {arg}" if arg else ""
    req = "req" if params else "nil"
    if resp:
        body = [
            f"func (c *Client) {name}(ctx context.Context{argpart}) ({resp}, error) {{",
            f"\tvar resp {resp}",
            f"\tif err := c.transport.Call(ctx, protocol.{const}, {req}, &resp); err != nil {{",
            f"\t\treturn {resp}{{}}, err",
            "\t}",
            "\treturn resp, nil",
            "}",
        ]
    else:
        body = [
            f"func (c *Client) {name}(ctx context.Context{argpart}) error {{",
            f"\treturn c.transport.Call(ctx, protocol.{const}, {req}, nil)",
            "}",
        ]
    return "\n".join(doc + body) + "\n"


def _render_test_with_arg(name: str, arg_type: str | None, multi: bool, wire: str) -> str:
    """A test case for a method whose signature was read from the real source."""
    if arg_type is None:
        arg = ""
    elif arg_type in _LITERAL_ARGS:
        arg = f", {_LITERAL_ARGS[arg_type]}"
    elif arg_type.startswith("*"):
        arg = ", nil"
    else:
        arg = f", *new(codexgo.{arg_type})"
    call = f"c.{name}(ctx{arg})"
    # A method may return just an error, or (value, error).
    stmt = f"\t\t\t_, err := {call}" if multi else f"\t\t\terr := {call}"
    return "\n".join([
        f'\t\t{{"{name}", "{wire}", func(ctx context.Context, c *codexgo.Client) error {{',
        stmt,
        "\t\t\treturn err",
        "\t\t}},",
    ])


def _render_case(name: str, const: str, params: str | None, wire: str) -> str:
    # `*new(T)` rather than `T{}`: some params types are aliases to a pointer or a map (the
    # Nullable* family), for which a composite literal is invalid.
    arg = f", *new(codexgo.{params})" if params else ""
    return "\n".join([
        f'\t\t{{"{name}", "{wire}", func(ctx context.Context, c *codexgo.Client) error {{',
        f"\t\t\t_, err := c.{name}(ctx{arg})",
        "\t\t\treturn err",
        "\t\t}},",
    ])


if __name__ == "__main__":
    raise SystemExit(main())
