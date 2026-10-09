#!/usr/bin/env python3
"""Check that no SDK enum invents values upstream does not accept.

The bug this exists for: the SDK had its own `ApprovalMode` with values
`deny_all | auto_review | on-request | never`. Upstream has two separate enums --
`AskForApproval` (`untrusted | on-request | never`) and `ApprovalsReviewer` (`user |
auto_review | guardian_subagent`). So the SDK had merged them and added `deny_all`, which is
valid in NEITHER, and the parameter was unusable for two independent reasons.

`type_check.py` structurally cannot see this: it compares type NAMES, and `ApprovalMode` is
simply not an upstream name, so there was nothing to compare it against. The values are the
evidence, so this checks values.

A single coincidentally-shared word means nothing -- `ThreadStatus` and `TurnItemsView` both
contain "notLoaded" and are unrelated -- hence the threshold. Flagging that would be noise, and
noise is what gets a gate switched off.

Usage:
    python3 scripts/enum_value_check.py report
    python3 scripts/enum_value_check.py check    # exit 1 when something is flagged
"""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import sys

SDK = pathlib.Path(__file__).resolve().parent.parent
SCHEMA_DIR = SDK / "internal/protocol/schema"
AGGREGATE = SCHEMA_DIR / "codex_app_server_protocol.v2.schemas.json"

# How much overlap with one upstream enum counts as "this is that concept". Two shared values and
# at least half the SDK enum's own values: below that it is coincidence.
MIN_SHARED = 2
MIN_SHARE = 0.5


def upstream_enums() -> dict[str, set[str]]:
    defs = json.loads(AGGREGATE.read_text())["definitions"]
    out: dict[str, set[str]] = {}
    for name, d in defs.items():
        if isinstance(d.get("enum"), list) and d["enum"] and all(
                isinstance(v, str) for v in d["enum"]):
            out[name] = set(d["enum"])
            continue
        for key in ("oneOf", "anyOf"):
            arms = d.get(key)
            if not isinstance(arms, list) or not arms:
                continue
            # Collect the string arms even when a sibling arm is an object. AskForApproval is
            # exactly that shape (`"never"` or `{"granular": ...}`), and skipping it made an
            # earlier version of this check blind to the very bug it was written for -- which
            # only the negative control revealed, since it built and reported success.
            values: set[str] = set()
            for arm in arms:
                if (isinstance(arm, dict) and arm.get("type") == "string"
                        and isinstance(arm.get("enum"), list)):
                    values.update(arm["enum"])
            if values:
                out[name] = values
    return out


def sdk_enums() -> dict[str, set[str]]:
    """Every SDK string enum, across all schema files (most are generated now)."""
    out: dict[str, set[str]] = {}
    for path in sorted(SCHEMA_DIR.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(errors="replace")
        for m in re.finditer(r'^type (\w+) string\n\nconst \(\n(.*?)^\)', text, re.M | re.S):
            name, body = m.group(1), m.group(2)
            values = set(re.findall(rf'^\t{name}\w*\s+{name} = "([^"]+)"', body, re.M))
            if values:
                out.setdefault(name, set()).update(values)
    return out


def findings() -> list[tuple[str, str, list[str], list[str]]]:
    """(sdk enum, upstream enum it matches, shared values, values valid in neither)."""
    up = upstream_enums()
    out = []
    for name, values in sorted(sdk_enums().items()):
        if name in up:
            continue  # same name upstream: type_check compares it field by field
        best = None
        for uname, uvalues in up.items():
            shared = values & uvalues
            if len(shared) < MIN_SHARED or len(shared) / len(values) < MIN_SHARE:
                continue
            if best is None or len(shared) > len(best[1]):
                best = (uname, shared)
        if best:
            uname, shared = best
            out.append((name, uname, sorted(shared), sorted(values - up[uname])))
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("mode", choices=("report", "check"), nargs="?", default="report")
    args = ap.parse_args()

    found = findings()
    if not found:
        print("enum value check: no SDK enum carries values upstream does not accept")
        return 0

    print(f"enum value check: {len(found)} SDK enum(s) that look like an upstream concept "
          f"under a different name:\n")
    for name, uname, shared, foreign in found:
        print(f"  {name} ~ upstream `{uname}`")
        print(f"      shared : {shared}")
        print(f"      FOREIGN: {foreign}")
        if foreign:
            print("      -> these values are valid in NO upstream enum here. Either the SDK "
                  "merged\n         two enums, or it invented values the server will reject.")
        else:
            print("      -> an exact subset: the SDK re-invented an existing concept.")
        print()
    if args.mode == "check":
        print("enum value check: FAIL")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
