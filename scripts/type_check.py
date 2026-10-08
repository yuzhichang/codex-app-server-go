#!/usr/bin/env python3
"""Type-level reconciliation between the SDK's Go types and the vendored upstream schema.

Why this exists
---------------
`scripts/coverage_gate.py` is *method*-level: it can prove the SDK wires up only methods
upstream declares. It says nothing about types, so a struct that upstream renamed, reshaped
or never had is invisible to it. That blind spot hid 27 diverging structs in
`internal/protocol/schema` (plan finding I7): 16 pure naming differences
(upstream `*Params`/`*Response` vs the SDK's `*Request`/`*Result`), 5 SDK-invented types
with no upstream counterpart at all, and one true orphan (`ThreadRollbackRequest`).

The rule this enforces: **an SDK type either matches an upstream definition by name, or it
is listed in gen/type-allowlist.json with a written reason.** "Invent only when genuinely
necessary" is exactly the kind of rule that rots unless it is checked mechanically.

Commands
--------
  report    list drift (default; informational)
  check     exit non-zero if any drifting type is not allow-listed
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
SCHEMA_DIR = SDK_ROOT / "internal" / "protocol" / "schema"
AGGREGATE = SCHEMA_DIR / "codex_app_server_protocol.v2.schemas.json"

_STRUCT = re.compile(r"^type (\w+) struct", re.M)
# Types the SDK models locally rather than importing: the aggregate omits them, but they
# are still types the SDK legitimately has to name.
ALLOWLIST_FILE = GEN_DIR / "type-allowlist.json"


def upstream_definitions() -> set[str]:
    doc = json.loads(AGGREGATE.read_text())
    for key in ("definitions", "$defs"):
        if isinstance(doc.get(key), dict):
            return set(doc[key])
    raise SystemExit(f"no definitions container found in {AGGREGATE}")


def sdk_types() -> set[str]:
    out: set[str] = set()
    for path in SCHEMA_DIR.glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        out.update(_STRUCT.findall(path.read_text(errors="replace")))
    return out


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cmd", choices=("report", "check"), nargs="?", default="report")
    args = ap.parse_args(argv)

    defs = upstream_definitions()
    sdk = sdk_types()
    allow = {e["name"]: e["reason"] for e in json.loads(ALLOWLIST_FILE.read_text())} if ALLOWLIST_FILE.is_file() else {}

    drift = sorted(t for t in sdk if t not in defs)
    unlisted = [t for t in drift if t not in allow]
    stale_allow = sorted(n for n in allow if n not in drift)

    print(f"type check: {len(sdk)} SDK structs vs {len(defs)} upstream definitions")
    print(f"  allow-listed   : {len(allow)}")
    print(f"  drifting       : {len(drift)} ({len(unlisted)} unlisted)")

    if drift:
        print("\n  drifting types:")
        for t in drift:
            mark = "listed" if t in allow else "UNLISTED"
            reason = f" -- {allow[t]}" if t in allow else ""
            print(f"    [{mark:8s}] {t}{reason}")
    if stale_allow:
        print(f"\n  [warn] allow-list entries that no longer drift (drop them): {stale_allow}")

    if args.cmd == "report":
        return 0

    ok = True
    if unlisted:
        ok = False
        print(f"\n  [FAIL] {len(unlisted)} drifting type(s) without an allow-list entry: {unlisted}")
        print("         Either align the name with upstream, remove the type, or add it to")
        print(f"         {ALLOWLIST_FILE.relative_to(SDK_ROOT)} with a reason.")
    else:
        print("\n  [PASS] every SDK type either matches upstream or is allow-listed with a reason")
    if stale_allow:
        ok = False
        print(f"  [FAIL] stale allow-list entries: {stale_allow}")
    print("\ntype check: " + ("PASS" if ok else "FAIL"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
