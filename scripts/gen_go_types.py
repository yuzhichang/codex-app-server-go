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
GEN_DIR = SDK_ROOT / "gen"

_STRUCT = re.compile(r"^type (\w+)\b", re.M)


# Go types that already represent "absent" without an extra pointer.
_NILABLE = {"json.RawMessage", "map[string]any", "any", "struct{}"}

# Notifications whose schema type does not follow `<Variant>Notification`.
#
# `modelProvider/authRecoveryStarted` and `/authRecoveryCompleted` share ONE payload type,
# AuthRecoveryNotification{message, provider, threadId, turnId}. The payload carries no
# started/completed discriminator, so a client must switch on the method name -- decoding
# the body alone cannot tell the two events apart.
NOTIFICATION_TYPE_OVERRIDES = {
    "AuthRecoveryStarted": "AuthRecoveryNotification",
    "AuthRecoveryCompleted": "AuthRecoveryNotification",
}


# Go initialisms, so generated field names match the rest of the package. Without this the
# generator emits `RemotePluginId` next to the hand-written `RemotePluginID` for the very
# same concept. JSON tags keep the exact upstream wire spelling either way.
_INITIALISMS = {
    "id": "ID",
    "ids": "IDs",
    "url": "URL",
    "urls": "URLs",
    "uri": "URI",
    "uris": "URIs",
    "api": "API",
    "http": "HTTP",
    "json": "JSON",
    "uuid": "UUID",
    "cwd": "CWD",
    "ui": "UI",
    "ip": "IP",
    "ttl": "TTL",
}


def _split_words(raw: str) -> list[str]:
    """Split `remotePluginId` into words. CamelCase must be split, or no initialism ever
    matches (`remotePluginId` is a single token to a naive split)."""
    spaced = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", raw)
    spaced = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1 \2", spaced)
    return [p for p in re.split(r"[^0-9A-Za-z]+", spaced) if p]


def go_name(raw: str) -> str:
    """`_meta` -> `Meta`, `dataBase64` -> `DataBase64`, `remotePluginId` -> `RemotePluginID`."""
    parts = _split_words(raw)
    name = "".join(_INITIALISMS.get(p.lower(), p[:1].upper() + p[1:]) for p in parts) or "Field"
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
        # schemars wraps $refs in allOf/anyOf/oneOf in two shapes that both need unwrapping:
        #
        #   {"allOf": [{"$ref": X}]}                  -- a $ref that also carries a description
        #   {"anyOf": [{"$ref": X}, {"type":"null"}]} -- a *nullable* $ref
        #
        # Without this, every documented path field and every nullable reference degrades to
        # json.RawMessage, which is both unergonomic and loses the type.
        for key in ("allOf", "anyOf", "oneOf"):
            variants = spec.get(key)
            if not isinstance(variants, list):
                continue
            non_null = [
                v for v in variants
                if not (isinstance(v, dict) and v.get("type") == "null")
            ]
            nullable = len(non_null) != len(variants)
            if len(non_null) == 1 and isinstance(non_null[0], dict):
                inner = go_type(non_null[0], defs, optional)
                # Types that are already nil-able do not need a pointer to express null.
                if nullable and not inner.startswith("*") and inner not in _NILABLE:
                    inner = "*" + inner
                return inner
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
        # A typed map (`additionalProperties: {$ref: X}`) should keep its value type;
        # collapsing it to map[string]any loses the shape callers need. ConfigReadResponse
        # .origins is the motivating case: map[string]ConfigLayerMetadata, whose `version`
        # is what optimistic concurrency (expectedVersion) is read from.
        addl = spec.get("additionalProperties")
        if isinstance(addl, dict):
            ref = addl.get("$ref")
            if isinstance(ref, str):
                return "map[string]" + ref.split("/")[-1]
            inner = go_type(addl, defs, False)
            if inner != "struct{}":
                return "map[string]" + inner
        # An object with no properties is an upstream empty response struct (`{}`), not a
        # free-form map.
        if not isinstance(spec.get("properties"), dict) and addl is None:
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
    ap.add_argument("--match", help="regex over definition names")
    ap.add_argument("--from-surface", action="store_true",
                    help="generate every params/response type used by a non-experimental method")
    ap.add_argument("--out", required=True, help="Go file to write")
    ap.add_argument("--schema", default=str(DEFAULT_SCHEMA))
    args = ap.parse_args()

    if not args.match and not args.from_surface:
        ap.error("one of --match or --from-surface is required")

    schema = json.loads(Path(args.schema).read_text())
    defs = schema.get("definitions") or {}
    rx = re.compile(args.match) if args.match else None

    dest = Path(args.out)
    if not dest.is_absolute():
        dest = SDK_ROOT / dest

    # Never emit a type the package already declares. `client_types_gen.go` still holds the
    # hand-written subset, and a handful of those (e.g. PluginSkillReadParams) already match
    # upstream -- re-emitting them would not compile. Skipped names are still traversed, so
    # references from generated types resolve to the existing declaration.
    existing: set[str] = set()
    if dest.parent.is_dir():
        for path in dest.parent.glob("*.go"):
            if path.resolve() == dest.resolve() or path.name.endswith("_test.go"):
                continue
            existing.update(_STRUCT.findall(path.read_text(errors="replace")))

    # Generate the matched definitions plus anything they transitively reference, so the
    # output compiles on its own.
    if args.from_surface:
        surface = json.loads((GEN_DIR / "method-surface.json").read_text())["methods"]
        wanted_names: set[str] = set()
        for m in surface:
            if m.get("experimental"):
                continue
            for key in ("params_type", "response_type"):
                name = m.get(key)
                if name and name in defs:
                    wanted_names.add(name)
            # Notification payloads too, so decoder cases have a type to decode into.
            if m.get("face") == "server_notification":
                guessed = m["variant"] + "Notification"
                if guessed in defs:
                    wanted_names.add(guessed)
                elif m["variant"] in NOTIFICATION_TYPE_OVERRIDES:
                    wanted_names.add(NOTIFICATION_TYPE_OVERRIDES[m["variant"]])
        wanted: set[str] = {n for n in wanted_names if n not in existing}
    else:
        wanted = {n for n in defs if rx.search(n) and n not in existing}
    pending = list(wanted)
    while pending:
        n = pending.pop()
        for ref in re.findall(r'"#/definitions/([^"]+)"', json.dumps(defs.get(n, {}))):
            # Respect `existing` here too: a type that was skipped because the package
            # already declares it must not be dragged back in as a transitive dependency
            # (that produced duplicate AbsolutePathBuf / SkillSummary declarations).
            if ref in defs and ref not in wanted and ref not in existing:
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

    dest.write_text("\n".join(header + body))
    print(f"wrote {dest.relative_to(SDK_ROOT)}: {len(wanted)} definitions")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
