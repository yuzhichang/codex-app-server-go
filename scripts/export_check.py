#!/usr/bin/env python3
"""Check that every type reachable through an exported struct is nameable by a caller.

A field of an exported struct is unusable if its TYPE cannot be named from outside the module.
The generated types live in `internal/protocol/schema`, which an external caller cannot import,
so any such type appearing in an exported struct's field must be re-exported by the root
package.

This is the class of bug that silently broke `ThreadListParams.CWD` and
`Config.ForcedChatgptWorkspaceIds`: they were `= json.RawMessage`, hence constructible, and
turning them into internal structs made both fields unsettable without anything failing.

Usage:
    python3 scripts/export_check.py report   # list gaps
    python3 scripts/export_check.py check     # exit 1 on any gap
"""

from __future__ import annotations

import argparse
import pathlib
import re
import sys

SDK = pathlib.Path(__file__).resolve().parent.parent
SCHEMA = SDK / "internal/protocol/schema"

# Files that make up the public surface: a type is nameable by a caller if it is declared or
# aliased in one of these.
PUBLIC_FILES = [
    SDK / "types.go",
    SDK / "interaction.go",
    SDK / "client.go",
    SDK / "turn.go",
    SDK / "thread.go",
    SDK / "events.go",
    SDK / "errors.go",
    SDK / "internal/protocol/types.go",
    SDK / "internal/protocol/defs.go",
]

# Go builtins / stdlib types that are always nameable.
IGNORED = {
    "RawMessage", "Error", "Time", "Duration", "Value", "Any",
    "string", "int", "int64", "float64", "bool", "byte", "rune",
}


def public_names() -> set[str]:
    """Every type name a caller can write."""
    names: set[str] = set()
    for path in PUBLIC_FILES:
        if not path.is_file():
            continue
        text = path.read_text(errors="replace")
        # Alias groups: `Name = other.Type` (optionally exported via a `type` block).
        names.update(re.findall(r"^\t(\w+)\s+=", text, re.M))
        names.update(re.findall(r"^\t(\w+)\s+[\w.\[\]*]+$", text, re.M))
        # Explicit `type Name = ...` and `type Name ...`.
        names.update(re.findall(r"^type (\w+)", text, re.M))
    return names


def schema_decls() -> dict[str, str]:
    """Every type declared in the schema package -> the file declaring it."""
    decls: dict[str, str] = {}
    for path in sorted(SCHEMA.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for m in re.finditer(r"^type (\w+)\b", path.read_text(errors="replace"), re.M):
            decls.setdefault(m.group(1), path.name)
    return decls


# A field line looks like: `\tName Type `json:"..."`` (possibly with a pointer/slice prefix).
FIELD = re.compile(r"^\s*(\w+)\s+(\*{0,2})(\[\])?(\*)?([A-Za-z_]\w*)\s+`")


def structs() -> list[tuple[str, str, str]]:
    """(file, struct name, body) for every struct in the schema package."""
    out = []
    for path in sorted(SCHEMA.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(errors="replace")
        for m in re.finditer(r"^type (\w+) struct \{\n(.*?)^\}", text, re.M | re.S):
            out.append((path.name, m.group(1), m.group(2)))
    return out


def gaps() -> list[tuple[str, str, str, str]]:
    """Fields a caller cannot set, because the field's type cannot be named from outside.

    Scoped to *Params structs deliberately. A caller has to WRITE the type name to populate an
    input field, so an unnameable type makes the field unreachable. An output field is only ever
    read, and Go's type inference means the name never has to be written:

        for _, t := range resp.Data { ... }   // never names the element type
        if err.CodexErrorInfo != nil { ... }  // never names CodexErrorInfo

    So reporting output fields would be 52 lines of noise over real code, and a gate nobody can
    act on is a gate that gets turned off.
    """
    public = public_names()
    decls = schema_decls()
    found = []
    for _file, parent, body in structs():
        if parent not in public or not parent.endswith("Params"):
            continue
        for line in body.splitlines():
            m = FIELD.match(line)
            if not m:
                continue
            field, stars, slice_, ptr, typ = m.groups()
            if typ in IGNORED or typ not in decls:
                continue
            if typ in public:
                continue
            found.append((parent, field, (stars or "") + (slice_ or "") + (ptr or "") + typ,
                          decls[typ]))
    return sorted(found)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("mode", choices=("report", "check"), nargs="?", default="report")
    args = ap.parse_args()

    found = gaps()
    if not found:
        print("export check: every *Params field type is nameable by a caller")
        return 0

    print(f"export check: {len(found)} *Params field(s) whose type a caller CANNOT name:\n")
    for parent, field, typ, src in found:
        print(f"  {parent}.{field}: {typ}   (declared in {src})")
    print("\n  Re-export the type in types.go, or the field cannot be set from outside.")
    if args.mode == "check":
        print("\nexport check: FAIL")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
