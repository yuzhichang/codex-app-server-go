#!/usr/bin/env python3
"""Field-level reconciliation: do the SDK's hand-written structs match the upstream schema?

Why this exists
---------------
`type_check.py` compares type *names*. That is not enough, and the gap hid a real bug.

The SDK is mid-migration: `internal/protocol/schema/client_types_gen.go` still holds ~50
hand-written structs while the rest of the package is generated from the vendored schema.
Comparing names said everything was aligned. Comparing *shapes* shows 31 of 45 shared types
differ, including:

  TurnStartParams.input    hand-written `string`     upstream `Vec<UserInput>` (REQUIRED)
  ReviewStartParams        hand-written `turnId`     upstream `target` (REQUIRED) + `delivery`
  TurnSteerParams.input    hand-written `string`     upstream `Vec<UserInput>`
  GitInfo                  invented field names      upstream `originUrl` / `sha`

`TurnStartParams` matters most: `SessionThread.Run` -- the SDK's primary entry point --
builds `TurnStartParams{Input: input}` from a Go string, so it sends `{"input":"hello"}`
where upstream requires `{"input":[{"type":"text","text":"hello"}]}`. Mock-server tests
could not catch it because they assert against the same hand-written type, and real-server
tests are skipped without a binary.

A name-only gate cannot see any of this. This script can.

  python3 scripts/type_shape_check.py report   # list every diverging field (default)
  python3 scripts/type_shape_check.py check    # non-zero if any divergence is unlisted

Divergences are allow-listed in gen/type-shape-allowlist.json with a reason and a status, so
the known ones stay visible and a new one cannot slip in unnoticed:

  deliberate  reviewed, and the divergence is correct (a documented design decision, or a
              type nothing can reach)
  pending     known, and still awaiting a decision -- surface the upstream fields, or
              allow-list it for good

Both keep the gate green. Only an UNLISTED divergence fails. The split exists so the gate
can be a tripwire without pretending the pending work is finished; `report` prints the
counts.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
SDK_ROOT = SCRIPT_DIR.parent
GEN_DIR = SDK_ROOT / "gen"
SCHEMA_DIR = SDK_ROOT / "internal" / "protocol" / "schema"
AGGREGATE = SCHEMA_DIR / "codex_app_server_protocol.v2.schemas.json"
HAND_WRITTEN = SCHEMA_DIR / "client_types_gen.go"
ALLOWLIST = GEN_DIR / "type-shape-allowlist.json"

_STRUCT = re.compile(r"^type (\w+) (?:struct|=) ?(.*)$", re.M)


def parse_structs(text: str) -> dict[str, list[tuple[str, str, str]]]:
    """name -> ordered (field, type, tag) triples. Embedded/anonymous lines are kept as-is."""
    out: dict[str, list[tuple[str, str, str]]] = {}
    for m in re.finditer(r"^type (\w+) struct \{\n(.*?)^\}", text, re.M | re.S):
        name, body = m.group(1), m.group(2)
        fields: list[tuple[str, str, str]] = []
        for line in body.splitlines():
            s = line.strip()
            if not s or s.startswith("//"):
                continue
            fm = re.match(r"(\w+)\s+([^\s`]+)\s+`([^`]*)`", s)
            fields.append((fm.group(1), fm.group(2), fm.group(3)) if fm else ("<embedded>", s, ""))
        out[name] = fields
    return out


def upstream_definitions() -> dict:
    doc = json.loads(AGGREGATE.read_text())
    for key in ("definitions", "$defs"):
        if isinstance(doc.get(key), dict):
            return doc[key]
    raise SystemExit(f"no definitions container found in {AGGREGATE}")


def generate(names: list[str]) -> dict[str, list[tuple[str, str, str]]]:
    """Ask the real generator what these definitions should look like."""
    regex = "^(" + "|".join(names) + ")$"
    with tempfile.TemporaryDirectory() as tmp:
        out = Path(tmp) / "generated.go"
        subprocess.run(
            [sys.executable, str(SCRIPT_DIR / "gen_go_types.py"), "--match", regex, "--out", str(out)],
            check=True, capture_output=True,
        )
        subprocess.run(["gofmt", "-w", str(out)], check=True, capture_output=True)
        return parse_structs(out.read_text())


def divergences() -> dict[str, list[str]]:
    hand = parse_structs(HAND_WRITTEN.read_text())
    defs = upstream_definitions()
    shared = sorted(n for n in hand if n in defs)
    gen = generate(shared)

    out: dict[str, list[str]] = {}
    for name in shared:
        h, g = hand[name], gen.get(name)
        if g is None or h == g:
            continue
        hf = {f[0]: (f[1], f[2]) for f in h if f[0] != "<embedded>"}
        gf = {f[0]: (f[1], f[2]) for f in g if f[0] != "<embedded>"}
        notes: list[str] = []
        for k in sorted(set(hf) - set(gf)):
            notes.append(f"only-hand-written {k} {hf[k][0]} `{hf[k][1]}`")
        for k in sorted(set(gf) - set(hf)):
            notes.append(f"only-upstream    {k} {gf[k][0]} `{gf[k][1]}`")
        for k in sorted(set(hf) & set(gf)):
            if hf[k] != gf[k]:
                notes.append(f"differs          {k}: hand={hf[k][0]} upstream={gf[k][0]}")
        if notes:
            out[name] = notes
    return out


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cmd", choices=("report", "check"), nargs="?", default="report")
    args = ap.parse_args(argv)

    found = divergences()
    allow = {}
    if ALLOWLIST.is_file():
        allow = {e["name"]: e for e in json.loads(ALLOWLIST.read_text())}

    print(f"type shape check: {len(found)} struct(s) diverge from the upstream schema")
    for name in sorted(found):
        entry = allow.get(name)
        mark = "UNLISTED" if entry is None else entry.get("status", "listed")
        print(f"\n  [{mark:10s}] {name}")
        for note in found[name]:
            print(f"      {note}")
        if entry:
            print(f"      reason: {entry['reason']}")

    unlisted = [n for n in found if n not in allow]
    stale = [n for n in allow if n not in found]
    pending = sorted(n for n, e in allow.items() if e.get("status") == "pending" and n in found)

    # A tripwire, not a sign-off: an entry is "deliberate" when the divergence has been
    # reviewed and is correct, and "pending" when it is known and still awaiting a decision
    # (补齐 the field set, or allow-list it for good). Both keep the gate green; only an
    # UNLISTED entry fails, so a new divergence cannot appear silently.
    print(f"\n  allow-listed: {len(allow) - len(pending)} deliberate, {len(pending)} pending")

    if args.cmd == "report":
        return 0

    ok = True
    if unlisted:
        ok = False
        print(f"\n  [FAIL] {len(unlisted)} diverging struct(s) without an allow-list entry: {unlisted}")
        print(f"         Either generate the type from the schema, or add it to {ALLOWLIST.name}")
        print("         with a reason and a status.")
    else:
        print("\n  [PASS] every diverging struct is allow-listed, with a reason")
    if stale:
        ok = False
        print(f"  [FAIL] stale allow-list entries (no longer diverge): {stale}")
    print("\ntype shape check: " + ("PASS" if ok else "FAIL"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
