#!/usr/bin/env python3
"""Generate the README's coverage tables from the audited artifacts.

Why generate them
-----------------
The previous table was hand-maintained and had drifted badly: it still advertised
`thread/rollback` (removed upstream), pinned the badge at codex-cli 0.142.0 while the SDK
is aligned to a much later commit, and quoted notification counts that no longer held. A
hand-written table describing machine-checked data will always rot; the numbers come from
`gen/method-surface.json` (what upstream declares) and `gen/implemented-methods.json` (what
the SDK actually wires up and tests), both of which are already enforced by
`make conformance-strict`.

Everything between the `coverage:start` / `coverage:end` markers in README.md is replaced,
so a re-run is idempotent and the surrounding prose stays hand-written.

  python3 scripts/gen_readme_coverage.py          # rewrite README.md in place
  python3 scripts/gen_readme_coverage.py --check   # exit 1 if README.md is out of date
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
SDK_ROOT = SCRIPT_DIR.parent
GEN_DIR = SDK_ROOT / "gen"
README = SDK_ROOT / "README.md"
SCHEMA_DIR = SDK_ROOT / "internal" / "protocol" / "schema"

START = "<!-- coverage:start -->"
END = "<!-- coverage:end -->"

# Method prefix -> the subsystem heading used in the table. Order matters: the first match
# wins, so more specific prefixes must come first.
SUBSYSTEMS: list[tuple[str, tuple[str, ...]]] = [
    ("Core / Initialize", ("initialize", "initialized", "serverRequest/")),
    ("Thread", ("thread/",)),
    ("Thread sections", ("threadSection/",)),
    ("Turn", ("turn/",)),
    ("Account / Login", ("account/",)),
    ("Models", ("model/", "modelProvider/")),
    ("Review", ("review/",)),
    ("Config", ("config/", "configRequirements/")),
    ("Skills", ("skills/",)),
    ("Plugins / Marketplace", ("plugin/", "marketplace/")),
    ("Apps", ("app/",)),
    ("Filesystem", ("fs/",)),
    ("MCP", ("mcpServer", "config/mcpServer")),
    ("Command exec", ("command/",)),
    ("Experimental features", ("experimentalFeature/",)),
    ("Hooks", ("hooks/",)),
    ("External agent config", ("externalAgentConfig/",)),
    ("Permissions", ("permissionProfile/",)),
    ("Windows sandbox", ("windowsSandbox/",)),
]


def subsystem_of(method: str) -> str:
    for name, prefixes in SUBSYSTEMS:
        if method.startswith(prefixes):
            return name
    return "Other"


def pinned_commit() -> str:
    """Read the anchor from the generated version.go rather than duplicating it here."""
    import re

    text = (SCHEMA_DIR / "version.go").read_text(errors="replace")
    found = re.search(r'SourceCodexCommit\s*=\s*"([^"]+)"', text)
    return found.group(1) if found else "unknown"


def load() -> tuple[list[dict], set[tuple[str, str]]]:
    surface = json.loads((GEN_DIR / "method-surface.json").read_text())["methods"]
    implemented = {(m["method"], m["face"]) for m in json.loads((GEN_DIR / "implemented-methods.json").read_text())}
    return surface, implemented


def build() -> str:
    surface, implemented = load()
    declared = [m for m in surface if not m.get("experimental")]
    whitelist = {(w["method"], w["face"]) for w in json.loads((GEN_DIR / "whitelist.json").read_text())}

    lines: list[str] = []
    lines.append(f"Aligned to codex commit `{pinned_commit()}`; scope = **stable** only.")
    lines.append("")
    lines.append(
        "Every number below is generated from `gen/method-surface.json` and "
        "`gen/implemented-methods.json`, and enforced by `make conformance-strict`."
    )
    lines.append("")

    # Client requests, grouped by subsystem.
    requests = [m for m in declared if m["face"] == "client_request"]
    grouped: dict[str, list[dict]] = {}
    for m in requests:
        grouped.setdefault(subsystem_of(m["method"]), []).append(m)

    lines.append("### Client → server requests")
    lines.append("")
    lines.append("| Subsystem | Implemented | Declared | Methods |")
    lines.append("|---|:---:|:---:|---|")
    for name, _ in SUBSYSTEMS:
        members = grouped.get(name)
        if not members:
            continue
        done = [m for m in members if (m["method"], m["face"]) in implemented]
        skipped = [m for m in members if (m["method"], m["face"]) in whitelist]
        # The method list shows what the SDK exposes (short name, prefix stripped) or an
        # explicit marker for the deliberately skipped ones.
        names = []
        for m in sorted(members, key=lambda x: x["method"]):
            short = m["method"]
            # Strip the subsystem's own prefix so the column stays readable, but only where
            # doing so is unambiguous: `marketplace/` is NOT stripped, because those methods
            # would then read as bare `add`/`remove` next to the plugin ones.
            for prefix in ("thread/", "turn/", "account/", "plugin/", "app/", "fs/"):
                if short.startswith(prefix):
                    short = short[len(prefix):]
                    break
            if (m["method"], m["face"]) in whitelist:
                names.append(f"~~`{short}`~~")
            else:
                names.append(f"`{short}`")
        lines.append(
            f"| {name} | {len(done)} | {len(members) - len(skipped)} | {' · '.join(names)} |"
        )

    known = {n for n, _ in SUBSYSTEMS}
    for key in sorted(k for k in grouped if k not in known):
        members = grouped[key]
        done = [m for m in members if (m["method"], m["face"]) in implemented]
        names = " · ".join(f"`{m['method']}`" for m in sorted(members, key=lambda x: x["method"]))
        lines.append(f"| {key} | {len(done)} | {len(members)} | {names} |")

    lines.append("")
    lines.append("### Server → client")

    notes = [m for m in declared if m["face"] == "server_notification"]
    notes_done = [m for m in notes if (m["method"], m["face"]) in implemented]
    reqs = [m for m in declared if m["face"] == "server_request"]
    reqs_done = [m for m in reqs if (m["method"], m["face"]) in implemented]

    lines.append("")
    lines.append("| Kind | Implemented | Declared |")
    lines.append("|---|:---:|:---:|")
    lines.append(f"| Notifications (typed decoders) | {len(notes_done)} | {len(notes)} |")
    lines.append(f"| Server-initiated requests (handlers) | {len(reqs_done)} | {len(reqs)} |")
    lines.append("")

    total_done = len([m for m in declared if (m["method"], m["face"]) in implemented])
    total_skipped = len([m for m in declared if (m["method"], m["face"]) in whitelist])
    lines.append(
        f"**Total: {total_done} of {len(declared) - total_skipped} in-scope methods implemented** "
        f"({total_skipped} deliberately not implemented, struck through above and in "
        f"`gen/whitelist.json` with a reason for each)."
    )
    lines.append("")
    lines.append(
        "Methods upstream marks `#[experimental]` are out of scope by decision R2 and are "
        "listed in `gen/not-in-scope.txt`."
    )
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--check", action="store_true", help="exit 1 if README.md is out of date")
    args = ap.parse_args(argv)

    block = build()
    text = README.read_text()
    if START not in text or END not in text:
        raise SystemExit(f"README.md is missing the {START} / {END} markers")

    head, rest = text.split(START, 1)
    _, tail = rest.split(END, 1)
    updated = f"{head}{START}\n\n{block}\n{END}{tail}"

    if args.check:
        if updated != text:
            print("README.md coverage block is out of date; run `make readme-coverage`")
            return 1
        print("README.md coverage block is up to date")
        return 0

    README.write_text(updated)
    print("README.md coverage block regenerated")
    return 0


if __name__ == "__main__":
    sys.exit(main())
