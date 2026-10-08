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


def _scalar_arm(spec: dict):
    """Representation for a non-object arm, or None if the arm is not a plain scalar.

    Returns (kind, go_type) where kind is string/integer/array.
    """
    if not isinstance(spec, dict):
        return None
    t = spec.get("type")
    if t == "string":
        return ("string", "string")
    if t == "integer":
        return ("integer", "int64")
    if t == "array":
        items = spec.get("items") or {}
        elem = items.get("$ref", "").split("/")[-1] or items.get("type")
        if not elem or not isinstance(elem, str):
            return None
        builtin = {"string": "string", "integer": "int64", "number": "float64", "boolean": "bool"}
        return ("array", "[]" + builtin.get(elem, go_name(elem)))
    return None


def _string_arm_union(spec: dict):
    """Arms of a union that CANNOT be a flat struct, because at least one arm is not an object.

    A struct always encodes as an object, so a union whose arms include a bare string, integer
    or array has no struct representation and needs its own JSON encoding. Returns the arms, or
    None when every arm is an object (the flat-struct strategies handle those).

    Also returns None when any arm is a shape this emitter cannot represent faithfully, so a
    working RawMessage is never replaced by a half-modelled type.
    """
    variants = None
    for key in ("oneOf", "anyOf"):
        if isinstance(spec.get(key), list) and spec[key]:
            variants = spec[key]
            break
    if variants is None:
        return None
    if all(_scalar_arm(v) is None and isinstance(v, dict) and "properties" in v for v in variants):
        return None
    # Arms are dispatched on their JSON token. Two arms can share a token only if they can be
    # told apart by content -- which is the case for serde's default "externally tagged"
    # representation: every object arm is a single required property whose NAME is the variant
    # tag ({"custom": ...} vs {"subAgent": ...}), so the keys identify the arm. Anything else
    # sharing a token is genuinely ambiguous and stays refused.
    groups: dict[str, list] = {}
    for v in variants:
        scalar = _scalar_arm(v)
        token = ("string" if scalar and scalar[0] == "string"
                 else "number" if scalar and scalar[0] == "integer"
                 else "array" if scalar else "object")
        groups.setdefault(token, []).append(v)

    for token, arms in groups.items():
        if len(arms) < 2:
            continue
        if token != "object":
            # Two arms sharing a scalar token cannot be told apart. The one case this affects,
            # CodexErrorInfo, is refused for a stronger reason as well: its trailing
            # `{"type": ["string","object"]}` arm is the Rust `Other` variant, which is
            # #[serde(untagged)] with a custom deserializer that accepts ANY string or object
            # and serializes back to the string "other". That behaviour lives in Rust code and
            # is not expressible in the schema, so a generated type would reject payloads
            # upstream accepts -- worse than the RawMessage it would replace.
            return None
        tags = []
        for arm in arms:
            required = arm.get("required") or []
            if not required:
                # Without a required property there is nothing to match on.
                return None
            tags.append(set(required))
        for i, left in enumerate(tags):
            for right in tags[i + 1:]:
                if left & right:
                    return None

    for v in variants:
        if _scalar_arm(v) is not None:
            continue
        if isinstance(v, dict) and v.get("$ref"):
            continue
        if isinstance(v, dict) and v.get("title") and isinstance(v.get("properties"), dict):
            continue
        return None
    return variants


def _emit_inline_struct(name: str, spec: dict, defs: dict, note: str) -> list[str]:
    """Emit an inline object schema as a named struct.

    An inline object has no name upstream, so the type mapper renders it as map[string]any --
    which throws away named fields. Naming it here keeps them: the name is derived
    deterministically from where it appears (parent type + property), so it is stable across
    regenerations.
    """
    required = set(spec.get("required") or [])
    out = [f"// {name} is the inline object {note}, named so its fields stay typed.", f"type {name} struct {{"]
    for raw, sub in (spec.get("properties") or {}).items():
        optional = raw not in required
        tag = raw + (",omitempty" if optional else "")
        out.append(f'\t{go_name(raw)} {go_type(sub, defs, optional)} `json:"{tag}"`')
    out.append("}")
    out.append("")
    return out


def _emit_object_arm(title: str, arm: dict, defs: dict) -> list[str]:
    """Emit the nested struct for one titled object arm of a string-arm union.

    A property that is itself an inline object gets a named struct too, rather than
    map[string]any: refusing those outright left several unions unmodelled.
    """
    required = set(arm.get("required") or [])
    out: list[str] = []
    fields: list[tuple[str, str, bool]] = []
    for raw, sub in (arm.get("properties") or {}).items():
        optional = raw not in required
        if isinstance(sub, dict) and isinstance(sub.get("properties"), dict):
            nested = title + go_name(raw)
            out.extend(_emit_inline_struct(nested, sub, defs, f"on {title}.{raw}"))
            fields.append((raw, "*" + nested, optional))
        else:
            fields.append((raw, go_type(sub, defs, optional), optional))
    out += [f"// {title} is the object arm of its union.", f"type {title} struct {{"]
    for raw, ftype, optional in fields:
        tag = raw + (",omitempty" if optional else "")
        out.append(f'\t{go_name(raw)} {ftype} `json:"{tag}"`')
    out.append("}")
    out.append("")
    return out


# Imports the emitters below need beyond encoding/json. Populated as definitions are emitted,
# because an unused import does not compile and goimports is not run on the output.
EXTRA_IMPORTS: set[str] = set()


def emit_string_arm_union(name: str, variants: list, defs: dict) -> list[str]:
    """Emit a union that carries its own JSON encoding.

    Scalar arms are typed; a titled object arm becomes a nested struct; a $ref becomes a pointer
    to the referenced type. Exactly one arm may be set -- enforced by the constructors and
    checked by the encoder, because a union that silently drops extra arms is worse than an
    untyped blob. Unmarshalling an arm that is not modelled is an error for the same reason.
    """
    EXTRA_IMPORTS.update({"bytes", "errors", "fmt"})
    fields: list[tuple[str, str]] = []
    kinds: list[str] = []
    nested: list[str] = []

    def add(field: str, gotype: str, described: str) -> None:
        if not any(f[0] == field for f in fields):
            fields.append((field, gotype))
            kinds.append(described)

    for v in variants:
        scalar = _scalar_arm(v)
        if scalar is not None:
            kind, gotype = scalar
            if kind == "string":
                add("String", "*string", "a string")
            elif kind == "integer":
                add("Number", "*int64", "an integer")
            else:
                add("List", gotype, "an array")
            continue
        if v.get("$ref"):
            ref = go_name(v["$ref"].split("/")[-1])
            add(ref, "*" + ref, "a " + ref)
            continue
        title = go_name(v["title"])
        nested.extend(_emit_object_arm(title, v, defs))
        add(title, "*" + title, "a " + title)

    out = list(nested)
    out += [
        f"// {name} is a union of {', '.join(kinds)}.",
        "//",
        "// Upstream declares it with at least one NON-object arm, so it cannot be modelled as a",
        "// struct with a discriminator the way the tagged unions are: a struct always encodes as",
        "// an object, which would lose the scalar form entirely. It therefore carries its own JSON",
        "// encoding. Exactly one arm is set; use the From... constructors.",
        f"type {name} struct {{",
    ]
    for field, gotype in fields:
        out.append(f"\t{field} {gotype}")
    out.append("}")
    out.append("")

    for field, gotype in fields:
        arg = gotype[1:] if gotype.startswith("*") else gotype
        out.append(f"// {name}From{field} builds the {field} arm.")
        if gotype.startswith("*"):
            out.append(f"func {name}From{field}(v {arg}) {name} {{ return {name}{{{field}: &v}} }}")
        else:
            out.append(f"func {name}From{field}(v {arg}) {name} {{ return {name}{{{field}: v}} }}")
        out.append("")

    def is_set(field: str, gotype: str) -> str:
        # Pointers for the scalars, so "set to zero" stays distinguishable from "not set".
        if gotype.startswith("*"):
            return f"u.{field} != nil"
        return f"len(u.{field}) > 0"

    out.append("// MarshalJSON encodes whichever arm is set, matching the upstream wire form.")
    out.append(f"func (u {name}) MarshalJSON() ([]byte, error) {{")
    out.append("\tset := 0")
    for field, gotype in fields:
        out.append(f"\tif {is_set(field, gotype)} {{")
        out.append("\t\tset++")
        out.append("\t}")
    out.append("\tif set != 1 {")
    out.append(f'\t\treturn nil, fmt.Errorf("{name}: exactly one arm must be set, got %d", set)')
    out.append("\t}")
    for field, gotype in fields:
        out.append(f"\tif {is_set(field, gotype)} {{")
        out.append(f"\t\treturn json.Marshal(u.{field})")
        out.append("\t}")
    out.append('\treturn nil, errors.New("unreachable")')
    out.append("}")
    out.append("")

    out.append("// UnmarshalJSON selects the arm by JSON token: a quoted string, a number, an array or")
    out.append("// an object.")
    out.append(f"func (u *{name}) UnmarshalJSON(data []byte) error {{")
    out.append("\ttrimmed := bytes.TrimSpace(data)")
    out.append("\tif len(trimmed) == 0 {")
    out.append(f'\t\treturn fmt.Errorf("{name}: empty payload")')
    out.append("\t}")
    # Object arms and the required property that tags each one, for content-based dispatch.
    # Collected before the decoder is written, since the decoder branches on whether several
    # arms share the object token.
    objects = [v for v in variants if _scalar_arm(v) is None and not v.get("$ref")]
    object_tags: dict[str, str] = {}
    for v in objects:
        required = list(v.get("required") or [])
        if required:
            object_tags[go_name(v["title"])] = required[0]

    multi_object = len(objects) > 1
    if multi_object:
        out.append("\t// Several arms are JSON objects; each is identified by its own required")
        out.append("\t// property, so probe for those keys rather than guessing by token alone.")
        out.append("\tvar probe map[string]json.RawMessage")
    out.append("\tswitch trimmed[0] {")

    branches: list[tuple[str, str, str]] = []  # (token, field, body)
    for field, gotype in fields:
        if gotype == "*string":
            branches.append(('"', field, f"\t\tvar v string\n\t\tif err := json.Unmarshal(data, &v); err != nil {{\n\t\t\treturn err\n\t\t}}\n\t\tu.{field} = &v"))
        elif gotype == "*int64":
            branches.append(("0-9", field, f"\t\tvar v int64\n\t\tif err := json.Unmarshal(data, &v); err != nil {{\n\t\t\treturn err\n\t\t}}\n\t\tu.{field} = &v"))
        elif gotype.startswith("[]"):
            branches.append(("[", field, f"\t\tvar v {gotype}\n\t\tif err := json.Unmarshal(data, &v); err != nil {{\n\t\t\treturn err\n\t\t}}\n\t\tu.{field} = v"))
        else:
            inner = gotype[1:]
            # Several object arms share the `{` token; each is identified by its own required
            # property, so the decoder probes for that key before choosing one.
            tag = object_tags.get(field)
            if tag is not None and len(objects) > 1:
                branches.append(("{", field, f"\t\tif _, ok := probe[\"{tag}\"]; ok {{\n\t\t\tvar v {inner}\n\t\t\tif err := json.Unmarshal(data, &v); err != nil {{\n\t\t\t\treturn err\n\t\t\t}}\n\t\t\tu.{field} = &v\n\t\t\treturn nil\n\t\t}}"))
            else:
                branches.append(("{", field, f"\t\tvar v {inner}\n\t\tif err := json.Unmarshal(data, &v); err != nil {{\n\t\t\treturn err\n\t\t}}\n\t\tu.{field} = &v"))

    modelled = set()
    object_group_emitted = False
    for token, field, body in branches:
        if token == "{" and multi_object:
            if object_group_emitted:
                continue
            object_group_emitted = True
            out.append("\tcase '{':")
            out.append("\t\tif err := json.Unmarshal(data, &probe); err != nil {")
            out.append("\t\t\treturn err")
            out.append("\t\t}")
            for _t, _f, b in [x for x in branches if x[0] == "{"]:
                out.append(f"\t\t// {_f} arm")
                for line in b.splitlines():
                    out.append(line)
            out.append("\t\t// No required key matched. Refusing is deliberate: guessing an arm")
            out.append("\t\t// would attribute the value to the wrong variant.")
            out.append(f'\t\treturn fmt.Errorf("{name}: no object arm matches %s", trimmed)')
            modelled.add(token)
            continue
        if token == "0-9":
            out.append("\tcase '-', '+':")
            out.append("\t\tfallthrough")
            out.append("\tcase '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':")
        else:
            out.append(f"\tcase {token!r}:")
        for line in body.splitlines():
            out.append(line)
        out.append("\t\treturn nil")
        modelled.add(token)

    out.append("\tdefault:")
    out.append("\t\t// No modelled arm matches. Refusing is deliberate: keeping nothing would drop a")
    out.append("\t\t// value the caller believes it received.")
    out.append(f'\t\treturn fmt.Errorf("{name}: unsupported arm %s", trimmed)')
    out.append("\t}")
    out.append("}")
    out.append("")
    return out


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

    arm_defs = _string_arm_union(spec)
    if arm_defs is not None:
        return emit_string_arm_union(name, arm_defs, defs)

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

    def build_header() -> list[str]:
        needed = sorted({"encoding/json"} | EXTRA_IMPORTS)
        if len(needed) == 1:
            import_block = [f'import "{needed[0]}"']
        else:
            import_block = ["import ("] + [f'\t"{n}"' for n in needed] + [")"]
        return [
            "// Code generated by scripts/gen_go_types.py from the vendored codex schema. DO NOT EDIT.",
            "//",
            "// Source: internal/protocol/schema/codex_app_server_protocol.v2.schemas.json",
            f"// Filter: {args.match}",
            "//",
            "// Regenerate: make generate-types",
            "",
            "package schema",
            "",
            *import_block,
            "",
        ]
    body: list[str] = []
    for name in sorted(wanted):
        body.extend(emit_definition(name, defs[name], defs))

    # Header last: EXTRA_IMPORTS is only complete once every definition has been emitted.
    dest.write_text("\n".join(build_header() + body))
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
