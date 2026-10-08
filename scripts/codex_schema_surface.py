#!/usr/bin/env python3
"""Extract and reconcile the Codex app-server protocol surface.

Why this exists
---------------
Upstream Codex ships **no protocol version number** (`codex-rs/Cargo.toml` is uniformly
`0.0.0` and `JSONRPC_VERSION` in `src/rpc.rs` is dead code).  The only honest anchor is
a **codex git commit plus artifact hashes**.

Two upstream artifacts describe the surface, and they disagree in ways that matter:

  1. The Rust source (`app-server-protocol/src/protocol/common.rs`) declares the surface
     and marks experimental entries with ``#[experimental("wire/name")]``.
  2. The precomputed exports (`schema/precomputed/app-server-exports-{stable,experimental}.json.zst`)
     are what the CLI actually decompresses and serves at runtime.

Measured facts at commit ``14c8b777`` that shape this tool:

  * ``--experimental`` DOES filter ClientRequest/ServerRequest (export exp-only == 65 / 1,
    exactly matching the source annotations).
  * ``--experimental`` does NOT filter ServerNotification: the stable and experimental
    exports are *set-equal* (84 == 84) and all 23 experimental notifications leak into
    the "stable" export.  Therefore the notification scope must be derived from the
    **source annotation**, never from the export mode.
  * The export DROPS 5 source methods that the source still declares.

So ``declared_stable`` == *source non-experimental*, and the exports are used for
reconciliation via the six set assertions in ``verify``.

Commands
--------
  sync    --codex-src <repo>   Regenerate vendored artifacts + gen/*.json.  Needs the repo.
  verify                       Run the six set assertions from checked-in artifacts (CI-safe:
                               needs no codex repo, no `zstandard`, no network).
  diff-cli --bundle <dir>      Compare a `codex app-server generate-json-schema` output
                               against the anchor.  Guards against a maintainer regenerating
                               from a CLI that is newer/older than the pinned commit.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

# --------------------------------------------------------------------------------------
# Layout
# --------------------------------------------------------------------------------------

SCRIPT_DIR = Path(__file__).resolve().parent
SDK_ROOT = SCRIPT_DIR.parent
GEN_DIR = SDK_ROOT / "gen"
VENDOR_DIR = SDK_ROOT / ".codex-schema"
EXPORTS_DIR = VENDOR_DIR / "exports"
SCHEMA_DIR = SDK_ROOT / "internal" / "protocol" / "schema"

AGGREGATED_SCHEMA = "codex_app_server_protocol.v2.schemas.json"
SOURCE_REL = "codex-rs/app-server-protocol/src/protocol/common.rs"
PRECOMPUTED_DIR_REL = "codex-rs/app-server-protocol/schema/precomputed"
AGGREGATED_JSON_REL = f"codex-rs/app-server-protocol/schema/json/{AGGREGATED_SCHEMA}"

FACES = ("client_request", "server_request", "server_notification", "client_notification")

# Macro invocation markers, in source order.
BLOCK_MARKERS = {
    "client_request": "client_request_definitions! {",
    "server_request": "server_request_definitions! {",
    "server_notification": "server_notification_definitions! {",
    "client_notification": "client_notification_definitions! {",
}

# The five methods that the source declares but the export drops, with the reason.
# The two notifications carry an explicit upstream marker:
#   common.rs:1977  /// This event is internal-only. Used by Codex Cloud.
#   common.rs:1979  /// This event is internal-only. Used by clients that need exact upstream usage.
EXPORT_EXCLUSIONS = [
    {
        "method": "getAuthStatus",
        "face": "client_request",
        "reason": "v1 deprecated top-level method; not emitted by the export",
    },
    {
        "method": "getConversationSummary",
        "face": "client_request",
        "reason": "v1 deprecated top-level method; not emitted by the export",
    },
    {
        "method": "gitDiffToRemote",
        "face": "client_request",
        "reason": "v1 deprecated top-level method; not emitted by the export",
    },
    {
        "method": "rawResponse/completed",
        "face": "server_notification",
        "reason": "internal-only (common.rs:1979 'This event is internal-only')",
    },
    {
        "method": "rawResponseItem/completed",
        "face": "server_notification",
        "reason": "internal-only (common.rs:1977 'This event is internal-only')",
    },
]


class SurfaceError(RuntimeError):
    pass


# --------------------------------------------------------------------------------------
# Source parsing
# --------------------------------------------------------------------------------------


def _blank_noise(text: str) -> str:
    """Blank out comments and string literals **in place, preserving length and newlines**.

    Length preservation is essential: the caller counts braces on the blanked copy but
    then slices the *original* text with the same indices, so every blanked character
    must occupy exactly the position of the character it replaced.
    """
    chars = list(text)
    i, n = 0, len(text)
    while i < n:
        c = text[i]
        if c == "/" and i + 1 < n and text[i + 1] == "/":
            while i < n and text[i] != "\n":
                chars[i] = " "
                i += 1
            continue
        if c == "/" and i + 1 < n and text[i + 1] == "*":
            chars[i] = chars[i + 1] = " "
            i += 2
            while i < n and not (text[i] == "*" and i + 1 < n and text[i + 1] == "/"):
                if text[i] != "\n":
                    chars[i] = " "
                i += 1
            if i < n:
                chars[i] = " "
                if i + 1 < n:
                    chars[i + 1] = " "
                i += 2
            continue
        if c == '"':
            chars[i] = " "
            i += 1
            while i < n:
                if text[i] == "\\":
                    chars[i] = " "
                    if i + 1 < n:
                        chars[i + 1] = " "
                    i += 2
                    continue
                if text[i] == '"':
                    chars[i] = " "
                    i += 1
                    break
                if text[i] != "\n":
                    chars[i] = " "
                i += 1
            continue
        i += 1
    return "".join(chars)


def _extract_braced_block(text: str, marker: str) -> str:
    """Return the body of `marker { ... }` (indices computed on a length-preserving blanked copy)."""
    start = text.find(marker)
    if start < 0:
        raise SurfaceError(f"macro marker not found: {marker!r}")
    open_idx = text.find("{", start + len(marker) - 1)
    if open_idx < 0:
        raise SurfaceError(f"no opening brace after {marker!r}")

    blanked = _blank_noise(text)
    depth = 0
    for i in range(open_idx, len(blanked)):
        if blanked[i] == "{":
            depth += 1
        elif blanked[i] == "}":
            depth -= 1
            if depth == 0:
                return text[open_idx + 1 : i]
    raise SurfaceError(f"unbalanced braces for {marker!r}")


def _camel(name: str) -> str:
    """serde `rename_all = "camelCase"` for a PascalCase variant name."""
    return name[:1].lower() + name[1:]


_ATTR = re.compile(r"^#\[(.*?)\]\s*(.*)$")
_EXPERIMENTAL = re.compile(r'^experimental\("([^"]+)"\)')
_RENAME = re.compile(r'rename\s*=\s*"([^"]+)"')
_STRUM = re.compile(r'serialize\s*=\s*"([^"]+)"')
# `Variant => "wire" {`  |  `Variant => "wire" (Type),`  |  `Variant {`  |  `Variant(Type),`
_VARIANT = re.compile(r'^([A-Z][A-Za-z0-9_]*)\s*(?:=>\s*"([^"]+)")?\s*[({]')
# bare unit variant: `Initialized,`
_VARIANT_BARE = re.compile(r"^([A-Z][A-Za-z0-9_]*)\s*,")


def parse_source(common_rs: str) -> list[dict]:
    """Parse the four macro invocations into a flat, ordered method list.

    Also captures each entry's `params`/`response` Rust type names, which the Go client
    generator needs to bind a typed method to the wire name.
    """
    methods: list[dict] = []
    for face, marker in BLOCK_MARKERS.items():
        block = _extract_braced_block(common_rs, marker)
        pending_exp: str | None = None
        pending_wire: str | None = None
        current: dict | None = None
        body: list[str] = []

        def flush():
            if current is None:
                return
            joined = "\n".join(body)
            current["params_type"] = _rust_type(joined, "params")
            current["response_type"] = _rust_type(joined, "response")
            methods.append(dict(current))

        for raw_line in block.splitlines():
            s = raw_line.strip()
            if not s or s.startswith("//"):
                continue
            while True:
                m = _ATTR.match(s)
                if not m:
                    break
                attr_body, s = m.group(1), m.group(2)
                exp = _EXPERIMENTAL.match(attr_body)
                if exp:
                    pending_exp = exp.group(1)
                for rx in (_RENAME, _STRUM):
                    found = rx.search(attr_body)
                    if found and pending_wire is None:
                        pending_wire = found.group(1)
                if not s:
                    break

            vm = _VARIANT.match(s) or _VARIANT_BARE.match(s)
            if vm:
                flush()
                variant = vm.group(1)
                explicit = vm.group(2) if vm.re is _VARIANT else None
                current = {
                    "method": explicit or pending_wire or _camel(variant),
                    "face": face,
                    "variant": variant,
                    "experimental": pending_exp is not None,
                    "experimental_reason": pending_exp,
                }
                body = [s]
                pending_exp = None
                pending_wire = None
                continue
            if current is not None:
                body.append(s)
        flush()
    return methods


# Capture to end of line: attributes on these lines contain commas, so a comma-delimited
# capture would stop inside `#[ts(optional, as = ...)]` and yield "optional" as the type.
_PARAMS = re.compile(r"\bparams\s*:\s*(.+)")
_RESPONSE = re.compile(r"\bresponse\s*:\s*(.+)")

# Words that can only come from an attribute, never a type name.
_ATTR_WORDS = {"optional", "nullable", "inline", "default", "undefined", "skip_serializing_if"}


def _rust_type(body: str, key: str) -> str | None:
    """Extract `params: v2::Foo` / `response: v2::Bar` and reduce it to the bare type name.

    Returns None when the field is absent, is a unit/`Option<()>` placeholder, or when the
    line only carries attributes -- i.e. the method takes no params.
    """
    m = (_PARAMS if key == "params" else _RESPONSE).search(body)
    if not m:
        return None
    raw = m.group(1).strip()
    raw = re.sub(r"#\[.*?\]", "", raw).strip()  # non-greedy: attributes may span the line
    if "Option<()>" in raw or raw in {"()", ""}:
        return None
    # Drop the trailing separator first: the macro entries end with a comma, so splitting
    # before trimming would leave an empty final segment and lose the type entirely.
    raw = raw.rstrip().rstrip(",").strip()
    # The type is the last path component, e.g. `v2::NullableFooParams` -> NullableFooParams.
    last = raw.split("::")[-1].strip()
    found = re.search(r"(\w+)", last)
    if not found:
        return None
    name = found.group(1)
    if name in {"Option", "Vec"} or name in _ATTR_WORDS:
        return None
    return name


# --------------------------------------------------------------------------------------
# Export parsing
# --------------------------------------------------------------------------------------


def _collect_methods(schema: dict) -> set[str]:
    """Collect method names from an internally-tagged enum schema.

    schemars encodes the tag as ``properties.method = {"enum": ["wire/name"]}``; some
    versions use ``const``.  Nested objects also carry a `method` key, hence the set union.
    """
    out: set[str] = set()

    def walk(node):
        if isinstance(node, dict):
            tag = node.get("method")
            if isinstance(tag, dict):
                enum = tag.get("enum")
                if isinstance(enum, list):
                    out.update(x for x in enum if isinstance(x, str))
                const = tag.get("const")
                if isinstance(const, str):
                    out.add(const)
            for value in node.values():
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    walk(schema)
    return out


def load_export_bundle(path: Path) -> dict[str, dict]:
    """Load a precomputed exports bundle (``.zst``) -> {filename: schema}."""
    try:
        import zstandard  # type: ignore
    except ImportError as exc:  # pragma: no cover - environment dependent
        raise SurfaceError(
            "reading .zst exports requires the `zstandard` module (pip install zstandard); "
            "`verify` does not need it — only `sync` does"
        ) from exc
    with path.open("rb") as fh:
        raw = zstandard.ZstdDecompressor().decompress(fh.read(), max_output_size=512 * 1024 * 1024)
    return json.loads(raw)["json_schema"]


def export_methods(bundle: dict[str, dict]) -> dict[str, set[str]]:
    return {
        face: _collect_methods(json.loads(bundle[f"{_schema_file(face)}"]))
        for face in FACES
    }


def _schema_file(face: str) -> str:
    return {
        "client_request": "ClientRequest.json",
        "server_request": "ServerRequest.json",
        "server_notification": "ServerNotification.json",
        "client_notification": "ClientNotification.json",
    }[face]


def load_method_set(path: Path) -> dict[str, set[str]]:
    data = json.loads(path.read_text())
    return {face: set(data.get(face, [])) for face in FACES}


def dump_method_set(sets: dict[str, set[str]]) -> str:
    return json.dumps({face: sorted(sets[face]) for face in FACES}, indent=2) + "\n"


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


# --------------------------------------------------------------------------------------
# sync
# --------------------------------------------------------------------------------------


def cmd_sync(args) -> int:
    repo = Path(args.codex_src).expanduser().resolve()
    source_path = repo / SOURCE_REL
    if not source_path.is_file():
        raise SurfaceError(f"codex source not found: {source_path}")

    try:
        commit = subprocess.run(
            ["git", "-C", str(repo), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        commit = "unknown"

    methods = parse_source(source_path.read_text())

    stable_bundle = export_methods(load_export_bundle(repo / PRECOMPUTED_DIR_REL / "app-server-exports-stable.json.zst"))
    exp_bundle = export_methods(load_export_bundle(repo / PRECOMPUTED_DIR_REL / "app-server-exports-experimental.json.zst"))

    # Vendor the aggregated stable schema (full types, needed for codegen in T1.2).
    agg_src = repo / AGGREGATED_JSON_REL
    if not agg_src.is_file():
        raise SurfaceError(f"aggregated schema not found: {agg_src}")
    SCHEMA_DIR.mkdir(parents=True, exist_ok=True)
    (SCHEMA_DIR / AGGREGATED_SCHEMA).write_bytes(agg_src.read_bytes())

    # Vendor the derived method sets (small; lets CI verify without zstandard or the repo).
    EXPORTS_DIR.mkdir(parents=True, exist_ok=True)
    (EXPORTS_DIR / "stable-methods.json").write_text(dump_method_set(stable_bundle))
    (EXPORTS_DIR / "experimental-methods.json").write_text(dump_method_set(exp_bundle))

    # Annotate each source method with its export presence.
    stable_all = set().union(*stable_bundle.values())
    exp_all = set().union(*exp_bundle.values())
    excluded = {(e["method"], e["face"]) for e in EXPORT_EXCLUSIONS}
    for entry in methods:
        key = (entry["method"], entry["face"])
        entry["in_export_stable"] = entry["method"] in stable_bundle[entry["face"]]
        entry["in_export_experimental"] = entry["method"] in exp_bundle[entry["face"]]
        entry["export_excluded"] = key in excluded

    GEN_DIR.mkdir(parents=True, exist_ok=True)
    (GEN_DIR / "method-surface.json").write_text(
        json.dumps(
            {
                "codex_commit": commit,
                "source": SOURCE_REL,
                "counts": {
                    "by_face": {face: sum(1 for m in methods if m["face"] == face) for face in FACES},
                    "experimental": sum(1 for m in methods if m["experimental"]),
                },
                "methods": methods,
            },
            indent=2,
        )
        + "\n"
    )
    (GEN_DIR / "export-exclusions.json").write_text(json.dumps(EXPORT_EXCLUSIONS, indent=2) + "\n")
    (GEN_DIR / "not-in-scope.txt").write_text(
        "# Derived: source methods marked #[experimental] -- out of scope by decision R2.\n"
        "# Regenerate with: make sync\n"
        + "\n".join(
            f"{m['face']:20s} {m['method']}"
            for m in sorted(methods, key=lambda m: (m["face"], m["method"]))
            if m["experimental"]
        )
        + "\n"
    )

    # --- version.go -------------------------------------------------------------------
    # Deterministic by construction: no timestamps, no counts derived from the host.
    # Idempotency matters because `sync` must be a no-op re-run for a clean `git diff`.
    declared_stable = {
        face: sum(1 for m in methods if m["face"] == face and not m["experimental"]) for face in FACES
    }
    source_experimental = {
        face: sum(1 for m in methods if m["face"] == face and m["experimental"]) for face in FACES
    }
    schema_rev = sha256_file(SCHEMA_DIR / AGGREGATED_SCHEMA)
    not_in_scope = sum(1 for m in methods if m["experimental"])
    version_go = f"""package schema

// Code generated by scripts/codex_schema_surface.py sync. DO NOT EDIT.
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
	SourceCodexCommit = "{commit}"

	// SchemaRevision is the sha256 of the vendored {AGGREGATED_SCHEMA}
	// (the upstream *stable* aggregate; it excludes the 5 methods listed in
	// gen/export-exclusions.json).
	SchemaRevision = "{schema_rev}"

	// SchemaTitle mirrors the upstream aggregate's title for traceability.
	SchemaTitle = "Codex app-server protocol v2 (stable)"

	// DeclaredStable counts source methods WITHOUT an #[experimental] annotation, per face.
	DeclaredStableClientRequest       = {declared_stable["client_request"]}
	DeclaredStableServerRequest       = {declared_stable["server_request"]}
	DeclaredStableServerNotification  = {declared_stable["server_notification"]}
	DeclaredStableClientNotification  = {declared_stable["client_notification"]}

	// SourceExperimental counts methods annotated #[experimental], per face. These are
	// out of scope by decision R2 (see gen/not-in-scope.txt).
	SourceExperimentalClientRequest      = {source_experimental["client_request"]}
	SourceExperimentalServerRequest      = {source_experimental["server_request"]}
	SourceExperimentalServerNotification = {source_experimental["server_notification"]}
	SourceExperimentalClientNotification = {source_experimental["client_notification"]}

	// NotInScopeTotal is DeclaredStable's complement: every #[experimental] method.
	NotInScopeTotal = {not_in_scope}
)
"""
    (SCHEMA_DIR / "version.go").write_text(version_go)

    manifest = {
        "codex_commit": commit,
        "generated_by": "scripts/codex_schema_surface.py sync",
        "scope_model": "declared_stable = source non-experimental; exports used for reconciliation",
        "artifacts": {
            str(p.relative_to(SDK_ROOT)): sha256_file(p)
            for p in (
                SCHEMA_DIR / AGGREGATED_SCHEMA,
                SCHEMA_DIR / "version.go",
                EXPORTS_DIR / "stable-methods.json",
                EXPORTS_DIR / "experimental-methods.json",
                GEN_DIR / "method-surface.json",
                GEN_DIR / "export-exclusions.json",
            )
        },
        "measured": {
            "source": {
                face: sum(1 for m in methods if m["face"] == face) for face in FACES
            },
            "source_experimental": {
                face: sum(1 for m in methods if m["face"] == face and m["experimental"]) for face in FACES
            },
            "export_stable": {face: len(stable_bundle[face]) for face in FACES},
            "export_experimental": {face: len(exp_bundle[face]) for face in FACES},
        },
        "declared_stable": {
            face: sum(1 for m in methods if m["face"] == face and not m["experimental"]) for face in FACES
        },
    }
    VENDOR_DIR.mkdir(parents=True, exist_ok=True)
    (VENDOR_DIR / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")

    if not stable_all and not exp_all:  # pragma: no cover - defensive
        raise SurfaceError("exports parsed empty; refusing to write artifacts")

    print(f"sync: codex {commit[:12]}")
    for face in FACES:
        print(
            f"  {face:20s} source={manifest['measured']['source'][face]:3d} "
            f"exp={manifest['measured']['source_experimental'][face]:3d} "
            f"declared_stable={manifest['declared_stable'][face]:3d} "
            f"export_stable={manifest['measured']['export_stable'][face]:3d} "
            f"export_exp={manifest['measured']['export_experimental'][face]:3d}"
        )
    print(f"  vendored -> {AGGREGATED_SCHEMA}, exports/*, gen/*")
    return 0


# --------------------------------------------------------------------------------------
# verify
# --------------------------------------------------------------------------------------


def _report(ok: bool, label: str, detail: str = "") -> bool:
    print(f"  [{'PASS' if ok else 'FAIL'}] {label}" + (f" — {detail}" if detail else ""))
    return ok


def cmd_verify(args) -> int:
    surface_doc = json.loads((GEN_DIR / "method-surface.json").read_text())
    methods = surface_doc["methods"]
    stable = load_method_set(EXPORTS_DIR / "stable-methods.json")
    exp = load_method_set(EXPORTS_DIR / "experimental-methods.json")
    exclusions = json.loads((GEN_DIR / "export-exclusions.json").read_text())

    all_src = {(m["method"], m["face"]) for m in methods}
    exp_all = {(m, face) for face, ms in exp.items() for m in ms}
    stable_all = {(m, face) for face, ms in stable.items() for m in ms}
    excl = {(e["method"], e["face"]) for e in exclusions}

    ok = True
    print(f"verify: codex {surface_doc.get('codex_commit', '?')[:12]}")

    # (1) source - export_experimental == the registered exclusion set
    diff = all_src - exp_all
    ok &= _report(
        diff == excl,
        "source - export_experimental == export-exclusions",
        f"n={len(diff)}" + (f"; unexpected={sorted(diff - excl)}; missing={sorted(excl - diff)}" if diff != excl else ""),
    )

    # (2) export_experimental - source == empty
    extra = exp_all - all_src
    ok &= _report(extra == set(), "export_experimental - source == {} ", f"extra={sorted(extra)}" if extra else "")

    # (3) per face: export_exp - export_stable == source experimental set
    for face in ("client_request", "server_request"):
        derived = exp[face] - stable[face]
        expected = {m["method"] for m in methods if m["face"] == face and m["experimental"]}
        ok &= _report(
            derived == expected,
            f"{face}: export_exp - export_stable == source experimental",
            f"derived={len(derived)} expected={len(expected)}"
            + (f"; diff={sorted(derived ^ expected)}" if derived != expected else ""),
        )

    # (4) notifications are NOT filtered by --experimental (known upstream behaviour)
    ok &= _report(
        stable["server_notification"] == exp["server_notification"],
        "server_notification: export_stable == export_experimental (known: notifications are not filtered)",
        f"stable={len(stable['server_notification'])} exp={len(exp['server_notification'])}",
    )

    # (5) export_stable(client_request) == declared_stable(client_request) - exclusions
    declared = {m["method"] for m in methods if m["face"] == "client_request" and not m["experimental"]}
    excl_cr = {e["method"] for e in exclusions if e["face"] == "client_request"}
    ok &= _report(
        stable["client_request"] == declared - excl_cr,
        "client_request: export_stable == declared_stable - exclusions",
        f"export={len(stable['client_request'])} declared={len(declared)} excl={len(excl_cr)}",
    )

    # (6) notification declared_stable arithmetic: stable == declared + leaked - internal
    declared_n = {m["method"] for m in methods if m["face"] == "server_notification" and not m["experimental"]}
    leaked = {m["method"] for m in methods if m["face"] == "server_notification" and m["experimental"]}
    internal = {e["method"] for e in exclusions if e["face"] == "server_notification"}
    ok &= _report(
        stable["server_notification"] == (declared_n | leaked) - internal,
        "server_notification: export_stable == declared_stable + leaked - internal",
        f"export={len(stable['server_notification'])} declared={len(declared_n)} leaked={len(leaked)} internal={len(internal)}",
    )

    print()
    if ok:
        print("verify: all assertions passed")
        return 0
    print("verify: FAILED — re-run `make sync` against the pinned codex commit, then review", file=sys.stderr)
    return 1


# --------------------------------------------------------------------------------------
# diff-cli
# --------------------------------------------------------------------------------------


def cmd_diff_cli(args) -> int:
    """Compare a CLI-generated bundle against the vendored anchor."""
    bundle = Path(args.bundle).expanduser().resolve()
    if not (bundle / "ClientRequest.json").is_file():
        raise SurfaceError(f"{bundle}/ClientRequest.json not found; pass a generate-json-schema --out dir")
    cli = {face: _collect_methods(json.loads((bundle / _schema_file(face)).read_text())) for face in FACES}
    stable = load_method_set(EXPORTS_DIR / "stable-methods.json")

    print(f"diff-cli: {bundle}")
    drifted = False
    for face in FACES:
        missing = stable[face] - cli[face]  # in anchor, absent from CLI
        extra = cli[face] - stable[face]  # in CLI, absent from anchor
        status = "OK" if not missing and not extra else "DRIFT"
        drifted |= bool(missing or extra)
        print(f"  [{status}] {face:20s} cli={len(cli[face]):3d} anchor_stable={len(stable[face]):3d}")
        if missing:
            print(f"         the CLI is BEHIND the anchor; missing {len(missing)}: {sorted(missing)[:8]}")
        if extra:
            print(f"         the CLI is AHEAD of the anchor; extra {len(extra)}: {sorted(extra)[:8]}")
    if drifted:
        print(
            "\ndiff-cli: the installed CLI does not match the pinned commit.\n"
            "  The anchor is the pinned commit, NOT the installed CLI. Regenerate the CLI or\n"
            "  re-pin deliberately (and re-run every review in the plan) before using its output.",
            file=sys.stderr,
        )
        return 1
    print("\ndiff-cli: installed CLI matches the pinned anchor")
    return 0


# --------------------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_sync = sub.add_parser("sync", help="regenerate vendored artifacts from a codex checkout")
    p_sync.add_argument("--codex-src", required=True, help="path to a github.com/openai/codex checkout")
    p_sync.set_defaults(func=cmd_sync)

    p_verify = sub.add_parser("verify", help="run set assertions from checked-in artifacts (CI)")
    p_verify.set_defaults(func=cmd_verify)

    p_diff = sub.add_parser("diff-cli", help="compare a CLI-generated bundle against the anchor")
    p_diff.add_argument("--bundle", required=True, help="output dir of `codex app-server generate-json-schema`")
    p_diff.set_defaults(func=cmd_diff_cli)

    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except SurfaceError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
