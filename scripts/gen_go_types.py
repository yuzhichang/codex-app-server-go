#!/usr/bin/env python3
"""Generate Go types from the vendored codex app-server schema (plan T1.2).

Scope note: this does NOT regenerate the whole protocol surface. `client_types_gen.go`
still holds ~55 hand-maintained structs covering the SDK's original subset, and replacing
them wholesale is a separate, larger change. This script generates the definitions the SDK
is *missing*, into a separate file, so new work (M2's fs/mcp, and later M5) is derived from
the schema instead of being transcribed by hand.

It reads only the vendored aggregate, so it needs no codex checkout and no network.

  python3 scripts/gen_go_types.py --match '^(Fs|Mcp|ConfigMcpServerReload)' \
      --out internal/protocol/schema/generated_fs_mcp.go
"""

from __future__ import annotations

import argparse
import json
import keyword
import re
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
SDK_ROOT = SCRIPT_DIR.parent
DEFAULT_SCHEMA = SDK_ROOT / "internal" / "protocol" / "schema" / "codex_app_server_protocol.v2.schemas.json"


def go_name(raw: str) -> str:
    """`_meta` -> `Meta`, `dataBase64` -> `DataBase64`, `fs/readFile` -> `FsReadFile`."""
    parts = [p for p in re.split(r"[^0-9A-Za-z]+", raw) if p]
    name = "".join(p[:1].upper() + p[1:] for p in parts) or "Field"
    if name[0].isdigit():
        name = "N" + name
    if keyword.iskeyword(name.lower()):
        name += "_"
    return name


def go_type(spec: dict, defs: dict, optional: bool) -> str:
    if not isinstance(spec, dict):
        return "json.RawMessage"

    ref = spec.get("$ref")
    if isinstance(ref, str):
        name = ref.split("/")[-1]
        base = name
        if optional and _is_struct(defs.get(name, {})):
            return "*" + base
        return base

    if "oneOf" in spec or "anyOf" in spec or "allOf" in spec:
        # A single-element allOf/anyOf/oneOf is just a wrapper around the real schema --
        # schemars emits `{"allOf": [{"$ref": "..."}]}` for every $ref that also carries a
        # description. Unwrap those, otherwise every documented path field would degrade
        # to json.RawMessage.
        for key in ("allOf", "anyOf", "oneOf"):
            variants = spec.get(key)
            if isinstance(variants, list) and len(variants) == 1 and isinstance(variants[0], dict):
                return go_type(variants[0], defs, optional)
        # A genuine union has no single Go representation; RawMessage keeps the bytes
        # intact and lets callers decode explicitly.
        return "json.RawMessage"

    if "enum" in spec:
        return go_name(_enum_type_name(spec)) if _enum_type_name(spec) else "string"

    t = spec.get("type")
    if isinstance(t, list):
        t = next((x for x in t if x != "null"), None)

    if t == "string":
        return "string"
    if t == "integer":
        return "int64"
    if t == "number":
        return "float64"
    if t == "boolean":
        return "bool"
    if t == "array":
        return "[]" + go_type(spec.get("items", {}), defs, False)
    if t == "object":
        # An object with no properties is an upstream empty response struct (`{}`), not a
        # free-form map.
        if not isinstance(spec.get("properties"), dict) and "additionalProperties" not in spec:
            return "struct{}"
        return "map[string]any"
    if t is None and isinstance(spec.get("properties"), dict):
        return "map[string]any"
    return "json.RawMessage"


def _is_struct(spec: dict) -> bool:
    return isinstance(spec.get("properties"), dict) or (spec.get("type") == "object")


def _enum_type_name(spec: dict) -> str:
    return spec.get("title") or ""


def emit_definition(name: str, spec: dict, defs: dict) -> list[str]:
    out: list[str] = []
    if "enum" in spec and isinstance(spec["enum"], list):
        values = [v for v in spec["enum"] if isinstance(v, str)]
        if values and all(isinstance(v, str) for v in spec["enum"]):
            out.append(f"// {name} mirrors the upstream `{name}` enum.")
            out.append(f"type {name} string")
            out.append("")
            out.append("const (")
            for v in values:
                out.append(f'\t{name}{go_name(v)} {name} = "{v}"')
            out.append(")")
            out.append("")
            return out

    props = spec.get("properties")
    if not isinstance(props, dict):
        # Not a struct and not a string enum (e.g. a bare alias) -- represent as an alias.
        out.append(f"// {name} mirrors the upstream `{name}` definition.")
        out.append(f"type {name} = {go_type(spec, defs, False)}")
        out.append("")
        return out

    required = set(spec.get("required") or [])
    out.append(f"// {name} mirrors the upstream `{name}` definition.")
    out.append(f"type {name} struct {{")
    for raw, sub in props.items():
        optional = raw not in required
        ftype = go_type(sub, defs, optional)
        tag = raw
        if optional:
            tag += ",omitempty"
        out.append(f"\t{go_name(raw)} {ftype} `json:\"{tag}\"`")
    out.append("}")
    out.append("")
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--match", default=".", help="regex over definition names")
    ap.add_argument("--out", required=True, help="Go file to write")
    ap.add_argument("--schema", default=str(DEFAULT_SCHEMA))
    args = ap.parse_args()

    schema = json.loads(Path(args.schema).read_text())
    defs = schema.get("definitions") or {}
    rx = re.compile(args.match)
    # Generate the matched definitions plus anything they transitively reference, so the
    # output compiles on its own.
    wanted: set[str] = {n for n in defs if rx.search(n)}
    pending = list(wanted)
    while pending:
        n = pending.pop()
        for ref in re.findall(r'"#/definitions/([^"]+)"', json.dumps(defs.get(n, {}))):
            if ref in defs and ref not in wanted:
                wanted.add(ref)
                pending.append(ref)

    header = [
        "// Code generated by scripts/gen_go_types.py from the vendored codex schema. DO NOT EDIT.",
        "//",
        "// Source: internal/protocol/schema/codex_app_server_protocol.v2.schemas.json",
        f"// Filter: {args.match}",
        "//",
        "// Regenerate: make generate-types",
        "",
        "package schema",
        "",
        "import \"encoding/json\"",
        "",
    ]
    body: list[str] = []
    for name in sorted(wanted):
        body.extend(emit_definition(name, defs[name], defs))

    dest = Path(args.out)
    if not dest.is_absolute():
        dest = SDK_ROOT / dest
    dest.write_text("\n".join(header + body))
    print(f"wrote {dest.relative_to(SDK_ROOT)}: {len(wanted)} definitions")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
