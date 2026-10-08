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


def _string_enum_union(spec: dict):
    """Return the merged values of a union whose arms are all string enums, else None.

    Upstream documents `AuthMode` as oneOf of five single-value enums rather than one enum
    with five values, and `ReasoningSummary` as two arms of three and one. The union of those
    values is exactly a string enum, so it can be emitted as one -- no behaviour is lost, and
    the type stops degrading to json.RawMessage.
    """
    variants = None
    for key in ("oneOf", "anyOf"):
        if isinstance(spec.get(key), list) and spec[key]:
            variants = spec[key]
            break
    if variants is None:
        return None
    values: list[str] = []
    for variant in variants:
        if not isinstance(variant, dict) or variant.get("type") != "string":
            return None
        enum = variant.get("enum")
        if not isinstance(enum, list) or not all(isinstance(v, str) for v in enum):
            return None
        for v in enum:
            if v not in values:
                values.append(v)
    return values or None


def _tagged_union(spec: dict):
    """Return the variants of a `type`-tagged object union, or None.

    A tagged union is a oneOf/anyOf where EVERY variant is an object carrying a `type`
    property with an enum. That shape has a faithful Go representation -- one flat struct with
    a discriminator field plus the union of the variants' fields -- so it does not have to
    degrade to json.RawMessage.

    Unions with a bare-string arm (AskForApproval is `"never"` or `{"granular":{...}}`) do NOT
    qualify: a struct always encodes as an object, so no struct can emit the string arm. Those
    need hand-written marshalling and stay RawMessage.
    """
    variants = None
    for key in ("oneOf", "anyOf"):
        if isinstance(spec.get(key), list) and spec[key]:
            variants = spec[key]
            break
    if variants is None:
        return None

    out = []
    for variant in variants:
        if not isinstance(variant, dict):
            return None
        props = variant.get("properties")
        if not isinstance(props, dict):
            return None
        # `type` is the usual discriminator, but upstream also uses `kind`
        # (FileSystemSpecialPath) and there is no reason to treat that as a different thing.
        for field in ("type", "kind"):
            tag = props.get(field)
            if isinstance(tag, dict) and isinstance(tag.get("enum"), list):
                values = [v for v in tag["enum"] if isinstance(v, str)]
                if len(values) == 1:
                    out.append((field, values[0], props, _flattened_props(variant)))
                    break
        else:
            return None
    # One discriminator for the whole union, or it is not a tagged union.
    if len({f for f, _, _, _ in out}) != 1:
        return None
    return out


def _flattened_props(variant: dict) -> dict:
    """Fields contributed by a variant's own `anyOf`/`oneOf`, not just its `properties`.

    Upstream expresses `#[serde(flatten)]` as a sibling anyOf: `UserInput.image` is
    `{"properties": {"type": "image", "detail": ...}, "anyOf": [{"url": ...}, {"fileId": ...}]}`.
    Reading only `properties` loses `url`/`fileId` entirely, which is how the first version of
    this emitter produced a UserInput that could not express an image-by-file-id at all.

    Arms that are not object shapes (a bare string, a $ref to one) are ignored here: they have
    no field names to contribute, and the variant's `properties` already carry its own.
    """
    out: dict = {}
    for key in ("anyOf", "oneOf"):
        for arm in variant.get(key) or []:
            if not isinstance(arm, dict):
                continue
            arm_props = arm.get("properties")
            if not isinstance(arm_props, dict):
                continue
            for field, sub in arm_props.items():
                if field == "type":
                    continue
                # First writer wins; a later arm restating the field is the same shape by
                # construction, and a genuine disagreement is caught by the merge below.
                out.setdefault(field, sub)
    return out


def _field_signature(spec: dict, defs: dict) -> str | None:
    """A stable signature for a variant field, or None when it must stay raw.

    Two variants that disagree about a field's type cannot both be served by one Go field, and
    a field that is itself a union has no single representation. Both cases fall back to
    json.RawMessage: that keeps the bytes and never invents a merge semantics upstream does
    not have. Only a field both variants agree on gets a typed Go field.
    """
    if not isinstance(spec, dict):
        return None
    for key in ("anyOf", "oneOf"):
        if isinstance(spec.get(key), list):
            arms = [a for a in spec[key] if not (isinstance(a, dict) and a.get("type") == "null")]
            if len(arms) == 1:
                # A nullable wrapper around one shape is not a union.
                spec = arms[0]
                break
            return None
    if spec.get("type") == "null":
        return None
    return go_type(spec, defs, False)


def emit_tagged_union(name: str, variants: list, defs: dict, tag_field: str = "type") -> list[str]:
    """Emit one flat discriminated struct for a tagged union.

    Field order is first-appearance across the variants, which is what the hand-written
    versions used and what the schema's own ordering implies. `required` is per-variant
    upstream and cannot be expressed by a single flat struct, so every variant field is
    omitempty; only the discriminator is always sent.
    """
    merged: dict[str, tuple[str, dict]] = {}
    order: list[str] = []
    for _field, _tag, props, flattened in variants:
        for field, sub in {**flattened, **props}.items():
            if field == tag_field:
                continue
            signature = _field_signature(sub, defs)
            if field not in merged:
                order.append(field)
                merged[field] = (signature, sub)
            elif merged[field][0] != signature:
                # Same name, different shape: keep the bytes, do not pick a winner.
                merged[field] = (None, sub)

    variant_tags = [tag for _, tag, _, _ in variants]
    lines = [
        f"// {name} mirrors the upstream `{name}` definition.",
        "//",
        f"// Upstream declares it as a `{tag_field}`-tagged union of {len(variants)} variants "
        f"({', '.join(sorted(variant_tags))}).",
        f"// It is modelled as one flat struct with an explicit {go_name(tag_field)} discriminator",
        "// plus the union of every variant's fields, so no field is dropped and no variant is",
        "// rejected.",
        "//",
        "// Fields that disagree in type across variants, or that are themselves unions, are passed",
        "// through as json.RawMessage rather than guessed at.",
        f"type {name} struct {{",
        f'\t{go_name(tag_field)} string `json:"{tag_field}"`',
    ]
    for field in order:
        signature, _ = merged[field]
        gofield = signature if signature is not None else "json.RawMessage"
        # The discriminator is always sent; everything else is conditional on which variant
        # is in play, which a flat struct cannot know.
        lines.append(f'\t{go_name(field)} {gofield} `json:"{field},omitempty"`')
    lines.append("}")
    lines.append("")

    lines.append(f"// {name} discriminator values, matching the upstream variant tags.")
    lines.append("const (")
    width = max(len(go_name(tag)) for tag in variant_tags)
    for tag in variant_tags:
        const = f"{name}{go_name(tag_field)}{go_name(tag)}"
        lines.append(f'\t{const:<{len(const) + width - len(go_name(tag))}} = "{tag}"')
    lines.append(")")
    lines.append("")
    return lines


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

    enum_values = _string_enum_union(spec)
    if enum_values is not None:
        out.append(f"// {name} mirrors the upstream `{name}` enum.")
        out.append(f"type {name} string")
        out.append("")
        out.append("const (")
        for v in enum_values:
            out.append(f'\t{name}{go_name(v)} {name} = "{v}"')
        out.append(")")
        out.append("")
        return out

    variant_defs = _tagged_union(spec)
    if variant_defs is not None:
        return emit_tagged_union(name, variant_defs, defs, tag_field=variant_defs[0][0])

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

    # Never EMIT a type the package already declares. `client_types_gen.go` still holds the
    # hand-written subset, and a handful of those (e.g. PluginSkillReadParams) already match
    # upstream -- re-emitting them would not compile.
    #
    # Traversal and emission are deliberately separate. Traversal follows references through
    # hand-written types as well, because a type whose only parent is hand-written would
    # otherwise never be reached: that silently made migrating such a type out of
    # client_types_gen.go impossible -- the generator emitted nothing and the package failed on
    # the undefined name. Emission still excludes them, which is what keeps duplicate
    # declarations from coming back.
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
        roots: set[str] = set(wanted_names)
    else:
        roots = {n for n in defs if rx.search(n)}

    # Traverse through everything reached, hand-written types included, so a type whose only
    # parent is hand-written is still discovered. Filtering by `existing` happens once, at the
    # end, which is what keeps duplicates out without also making those types unreachable.
    reached = set(roots)
    pending = list(roots)
    while pending:
        n = pending.pop()
        for ref in re.findall(r'"#/definitions/([^"]+)"', json.dumps(defs.get(n, {}))):
            if ref in defs and ref not in reached:
                reached.add(ref)
                pending.append(ref)

    wanted: set[str] = {n for n in reached if n not in existing}

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
    # relative_to raises when --out points outside the repo; fall back to the full path
    # rather than crashing after having already written the file.
    try:
        shown = dest.relative_to(SDK_ROOT)
    except ValueError:
        shown = dest
    print(f"wrote {shown}: {len(wanted)} definitions")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
