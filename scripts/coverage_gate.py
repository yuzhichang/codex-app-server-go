#!/usr/bin/env python3
"""Implementation-coverage gate for the codex app-server surface.

Why a *separate* implementation registry?
-----------------------------------------
`internal/protocol/envelope.go` holds `Method*` constants, and those constants are the
protocol's *declaration*: their mere presence proves nothing about whether the SDK wires
the method up. If the gate counted constants, then the moment the constants were generated
from the schema the gap would silently drop to zero -- with no `Client` method, no
dispatcher branch and no notification decoder behind them. That was review finding A1.

So coverage is judged against **live wiring evidence in real code**, plus a committed
registry (`gen/implemented-methods.json`) that must agree with that evidence in *both*
directions:

  * wiring exists but is unregistered  -> the registry is stale
  * registered but no wiring exists    -> the registry is lying

Three checks per entry (all required, per decision R14 / plan T1.1):

  1. wiring evidence, keyed by kind (never the generated constant file)
  2. test evidence: a named test references the method's constant
  3. two-way agreement between the registry and the evidence

Commands
--------
  report                      show the gap (default; safe to run mid-implementation)
  write                       regenerate gen/implemented-methods.json from live evidence
  check  [--strict]           verify the committed registry against live evidence;
                              --strict also fails when the declared/implemented gap is
                              non-empty (this is the CI mode)
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
SDK_ROOT = SCRIPT_DIR.parent
GEN_DIR = SDK_ROOT / "gen"

# Wiring evidence is located by *role*, not by a fixed allow-list of files. An earlier
# version enumerated the files that "obviously" held client methods, which silently stopped
# seeing any method implemented in a new file (the fs/mcp work in fs.go and mcp.go was
# invisible to the gate until this was fixed).
#
# Files that can never be evidence, regardless of role.
NON_EVIDENCE_FILES = {
    "internal/protocol/envelope.go",  # the declaration itself
}
NON_EVIDENCE_PREFIXES = (
    "internal/protocol/schema/",  # generated types
)

# Roles that must be evidenced from a specific layer, so that an incidental mention
# elsewhere does not count as wiring.
DECODER_FILES = ("events.go", "events_extra.go")
# `server_request_handler` means the *Dispatcher* answers the request, per T1.1: "Dispatcher
# .HandleServerRequest has a case". internal/protocol/decode.go only proves the params can be
# decoded, which is a different thing -- accepting it caused two decode-only methods
# (item/permissions/requestApproval, item/tool/requestUserInput) to be reported as
# implemented while nothing actually handled them.
HANDLER_FILES = ("interaction.go",)

# A client request must be evidenced by a call site outside the decoding/handling layer:
# appearing only in event decoders would mean it is declared but never called.
CLIENT_METHOD_EXCLUDE = set(DECODER_FILES) | set(HANDLER_FILES)

FACE_KIND = {
    "client_request": "client_method",
    "server_request": "server_request_handler",
    "server_notification": "notification_decoder",
    "client_notification": "client_notification_sender",
}


def not_implemented_text(whitelist: list[dict]) -> str:
    """Human-readable mirror of gen/whitelist.json: the declared-stable methods the SDK
    deliberately does not implement (decision R4). whitelist.json stays the machine-readable
    source of truth (it carries the reasons); this file exists so the plan's
    `gen/not-implemented.txt` artifact has a single, generated author."""
    lines = [
        "# Declared-stable methods this SDK deliberately does not implement (decision R4).",
        "# Authoritative machine-readable form (with reasons): gen/whitelist.json",
    ]
    for w in sorted(whitelist, key=lambda w: (w["face"], w["method"])):
        lines.append(f"{w['face']:20s} {w['method']}")
    return "\n".join(lines) + "\n"


_CONST_DECL = re.compile(r'^\s*(Method\w+)\s*=\s*"([^"]+)"', re.M)


def _iter_go_files() -> list[Path]:
    out = []
    for p in SDK_ROOT.rglob("*.go"):
        if "vendor" in p.parts or ".git" in p.parts:
            continue
        out.append(p)
    return out


def load_consts() -> dict[str, str]:
    """wire method name -> Go constant name.

    Scans every declaration file in the protocol package, not just envelope.go: method
    constants may also be generated (internal/protocol/generated_methods.go), and reading
    only envelope.go made every generated binding invisible to the gate.
    """
    out: dict[str, str] = {}
    for path in (SDK_ROOT / "internal" / "protocol").glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        out.update({wire: name for name, wire in _CONST_DECL.findall(path.read_text(errors="replace"))})
    return out


_IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
_STR = re.compile(r'"((?:[^"\\]|\\.)*)"')


def scan_files() -> tuple[dict[str, set[str]], dict[str, set[str]]]:
    """Return (identifiers, string literals) per file, via a cheap lexical scan.

    String literals matter for test evidence: the SDK's tests generally drive mock servers
    with wire method strings ("thread/start") rather than referencing the `Method*`
    constant, so looking only for constants would under-report test coverage badly.
    """
    idents: dict[str, set[str]] = {}
    strings: dict[str, set[str]] = {}
    for path in _iter_go_files():
        rel = path.relative_to(SDK_ROOT).as_posix()
        text = path.read_text(errors="replace")
        idents[rel] = set(_IDENT.findall(text))
        strings[rel] = set(_STR.findall(text))
    return idents, strings


def load_json(path: Path, default):
    if not path.is_file():
        return default
    return json.loads(path.read_text())


def _is_evidence_file(rel: str) -> bool:
    if rel in NON_EVIDENCE_FILES:
        return False
    if rel.endswith("_test.go") or rel in {"MEMORY.md", "Makefile"}:
        return False
    return not rel.startswith(NON_EVIDENCE_PREFIXES)


def _evidence_files(kind: str, const: str, idents: dict[str, set[str]]) -> list[str]:
    """Files that constitute wiring evidence for `kind` (see the role notes above)."""
    candidates = [rel for rel in idents if _is_evidence_file(rel)]
    if kind == "notification_decoder":
        pool = [rel for rel in candidates if rel in DECODER_FILES]
    elif kind == "server_request_handler":
        pool = [rel for rel in candidates if rel in HANDLER_FILES]
    elif kind == "client_method":
        pool = [rel for rel in candidates if rel not in CLIENT_METHOD_EXCLUDE]
    else:  # client_notification_sender -- anywhere a Notify call could live
        pool = candidates
    return sorted(rel for rel in pool if const in idents.get(rel, set()))


def build_evidence(
    consts: dict[str, str],
    face_by_wire: dict[str, str],
    idents: dict[str, set[str]],
    test_idents: dict[str, set[str]],
    test_strings: dict[str, set[str]],
):
    """wire -> (kind, wiring files, test files) for every method with real wiring.

    The expected kind is derived from the method's *face* rather than by scanning every
    role's file list. Scanning all roles is ambiguous: `client.go` hosts both client
    requests and the single client notification, so a request implemented there would be
    misattributed to `client_notification_sender` and then dropped by the Notify check.
    """
    evidence: dict[str, dict] = {}
    for wire, const in consts.items():
        face = face_by_wire.get(wire)
        if face:
            kind = FACE_KIND[face]
            hits = _evidence_files(kind, const, idents)
            if not hits:
                continue
            # A client notification must have an actual send (Notify) call site; a bare
            # reference is not evidence that anything is ever sent.
            if kind == "client_notification_sender":
                notify = re.compile(r"Notify\([^)]*" + re.escape(const))
                if not any(notify.search((SDK_ROOT / rel).read_text(errors="replace")) for rel in hits):
                    continue
        else:
            # Not declared upstream: classify across every role purely so the R3 removal
            # report can name what the stale wiring is. A stale binding is most often a
            # call site, so the client-method rule is the primary probe.
            kind = None
            hits = []
            for cand in ("client_method", "server_request_handler", "notification_decoder"):
                found = _evidence_files(cand, const, idents)
                if found:
                    kind, hits = cand, found
                    break
            if not hits:
                continue

        # Test evidence: the test references the constant OR the wire method string.
        tests_for = sorted(
            rel for rel in test_idents
            if const in test_idents[rel] or wire in test_strings.get(rel, ())
        )
        evidence[wire] = {
            "kind": kind,
            "face": face,
            "wiring": sorted(hits),
            "tests": tests_for,
        }
    return evidence


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cmd", choices=("report", "write", "check"))
    ap.add_argument("--strict", action="store_true", help="fail when the declared/implemented gap is non-empty")
    args = ap.parse_args(argv)

    surface = load_json(GEN_DIR / "method-surface.json", None)
    if surface is None:
        print("error: gen/method-surface.json missing; run `make sync`", file=sys.stderr)
        return 2

    whitelist = load_json(GEN_DIR / "whitelist.json", [])
    registered = load_json(GEN_DIR / "implemented-methods.json", [])

    consts = load_consts()
    idents, strings = scan_files()
    test_idents = {rel: toks for rel, toks in idents.items() if rel.endswith("_test.go")}
    test_strings = {rel: toks for rel, toks in strings.items() if rel.endswith("_test.go")}
    # Authoritative wire -> face map, straight from the extracted upstream surface.
    face_by_wire = {m["method"]: m["face"] for m in surface["methods"]}
    live = build_evidence(consts, face_by_wire, idents, test_idents, test_strings)

    # declared_stable: source methods WITHOUT an experimental annotation.
    declared = [
        {"method": m["method"], "face": m["face"]}
        for m in surface["methods"]
        if not m.get("experimental")
    ]
    declared_set = {(m["method"], m["face"]) for m in declared}
    whitelist_set = {(w["method"], w["face"]) for w in whitelist}

    # Implemented = declared AND live evidence AND a test (checks 1 + 2).
    implemented = []
    for m in declared:
        ev = live.get(m["method"])
        if not ev or ev["kind"] != FACE_KIND[m["face"]]:
            continue
        if not ev["tests"]:
            continue  # check 2 fails: no test evidence
        implemented.append({
            "method": m["method"],
            "face": m["face"],
            "kind": ev["kind"],
            "wiring": ev["wiring"],
            "test": ev["tests"][0],
        })
    implemented_set = {(m["method"], m["face"]) for m in implemented}

    gap = sorted(declared_set - whitelist_set - implemented_set)
    # Methods the SDK wires up that upstream no longer declares -> must be removed (R3).
    unknown = sorted(
        (wire, ev["kind"]) for wire, ev in live.items() if wire not in {m[0] for m in declared_set}
        and wire not in {m["method"] for m in surface["methods"]}
    )

    registered_set = {(r["method"], r["face"]) for r in registered}
    unregistered = sorted(implemented_set - registered_set)
    stale = sorted(registered_set - implemented_set)

    if args.cmd == "write":
        (GEN_DIR / "implemented-methods.json").write_text(
            json.dumps(sorted(implemented, key=lambda m: (m["face"], m["method"])), indent=2) + "\n"
        )
        (GEN_DIR / "unknown-methods.txt").write_text(
            "# Methods the SDK still wires up that upstream no longer declares (decision R3: remove them).\n"
            + "".join(f"{kind:24s} {wire}\n" for wire, kind in unknown)
        )
        (GEN_DIR / "not-implemented.txt").write_text(not_implemented_text(whitelist))
        print(f"write: {len(implemented)} implemented method(s) recorded")
        print(f"       gap={len(gap)} unknown(stale upstream)={len(unknown)}")
        return 0

    if args.cmd == "report":
        # Split the gap into "no wiring at all" and "wired but missing test evidence":
        # conflating them hides work that is nearly done behind work that has not started.
        untested = sorted(
            m for m in (declared_set - whitelist_set - implemented_set)
            if (ev := live.get(m[0])) and ev["kind"] == FACE_KIND[m[1]]
        )
        not_started = sorted((declared_set - whitelist_set - implemented_set) - set(untested))

        by_face: dict[str, list[str]] = {}
        for method, face in not_started:
            by_face.setdefault(face, []).append(method)

        print(f"coverage: declared_stable={len(declared_set)} whitelist={len(whitelist_set)} "
              f"implemented={len(implemented_set)} gap={len(gap)} "
              f"(not_started={len(not_started)} wired_but_untested={len(untested)})")
        print("\n  wired but MISSING TEST EVIDENCE (add a test to count these as implemented):")
        for m, face in untested:
            print(f"    - {m}  ({face})")
        for face in sorted(by_face):
            print(f"\n  {face} ({len(by_face[face])} not implemented):")
            for m in by_face[face]:
                print(f"    - {m}")
        if unknown:
            print(f"\n  wires-up-but-not-upstream ({len(unknown)}) -- migrate/remove per R3:")
            for wire, kind in unknown:
                print(f"    - {wire}  ({kind})")
        return 0

    # check
    ok = True
    print("coverage gate:")
    if unregistered:
        ok = False
        print(f"  [FAIL] wiring exists but is not registered ({len(unregistered)}): {unregistered[:5]}")
    else:
        print("  [PASS] every wired-up method is registered")
    if stale:
        ok = False
        print(f"  [FAIL] registered but no live wiring+test ({len(stale)}): {stale[:5]}")
    else:
        print("  [PASS] every registered method has live wiring and a test")
    # A whitelist entry that is not declared stable is a bookkeeping error: it would silently
    # excuse a method that does not exist (or is experimental), and skew the gate's arithmetic.
    bad_whitelist = sorted(whitelist_set - declared_set)
    if bad_whitelist:
        ok = False
        print(f"  [FAIL] whitelist lists methods that are not declared stable ({len(bad_whitelist)}): {bad_whitelist}")
    else:
        print("  [PASS] every whitelist entry is a declared-stable method")
    # A whitelisted method that is ALSO fully implemented is a contradiction: drop it from
    # the whitelist rather than carrying a stale excuse.
    redundant = sorted(whitelist_set & implemented_set)
    if redundant:
        ok = False
        print(f"  [FAIL] whitelisted but actually implemented (remove from gen/whitelist.json) ({len(redundant)}): {redundant}")
    else:
        print("  [PASS] no whitelist entry is contradicted by live wiring")
    if unknown:
        ok = False
        print(f"  [FAIL] wired up but no longer declared upstream (remove per R3) ({len(unknown)}): {[w for w, _ in unknown]}")
    else:
        print("  [PASS] nothing is wired up that upstream has removed")
    # not-implemented.txt is a generated mirror of whitelist.json; a mismatch means it was
    # hand-edited or the whitelist changed without regenerating it.
    ni_path = GEN_DIR / "not-implemented.txt"
    if not ni_path.is_file() or ni_path.read_text() != not_implemented_text(whitelist):
        ok = False
        print("  [FAIL] gen/not-implemented.txt is missing or stale (run `make coverage-write`)")
    else:
        print("  [PASS] gen/not-implemented.txt matches gen/whitelist.json")
    if args.strict and gap:
        ok = False
        print(f"  [FAIL] declared/implemented gap is non-empty ({len(gap)}); run `report` for the list")
    elif gap:
        print(f"  [warn] gap non-empty ({len(gap)}) -- expected mid-implementation; not fatal without --strict")
    else:
        print("  [PASS] declared_stable - whitelist - implemented == {}")
    print()
    print(f"coverage gate: " + ("PASS" if ok else "FAIL"))
    print(f"  (declared_stable={len(declared_set)} whitelist={len(whitelist_set)} "
          f"implemented={len(implemented_set)} gap={len(gap)})")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
