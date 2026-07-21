#!/usr/bin/env python3
"""Extract the transitive closure of selected root messages from a .proto file.

Emits a pruned proto with a renamed package so its messages register under
different full names — letting a second generated copy (e.g. for vtprotobuf
experiments) coexist in the same binary as the primary pogo package without
panicking the global proto registry.

Dependency detection is name-based: any identifier inside a kept block that
matches a top-level message/enum name pulls that definition in. This
over-includes (e.g. names in comments) but never under-includes, which is the
safe direction for codegen.
"""

import argparse
import re


def parse_blocks(text):
    """Return list of (kind, name, block_text) for top-level message/enum."""
    blocks = []
    i = 0
    pattern = re.compile(r"^(message|enum)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{", re.M)
    while True:
        m = pattern.search(text, i)
        if not m:
            break
        depth = 0
        j = m.end() - 1  # at the '{'
        while j < len(text):
            c = text[j]
            if c == "{":
                depth += 1
            elif c == "}":
                depth -= 1
                if depth == 0:
                    break
            j += 1
        blocks.append((m.group(1), m.group(2), text[m.start():j + 1]))
        i = j + 1
    return blocks


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--proto", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--roots", required=True, help="comma-separated root message names")
    ap.add_argument("--package", required=True, help="package name for the pruned file")
    args = ap.parse_args()

    with open(args.proto) as f:
        text = f.read()

    blocks = parse_blocks(text)
    by_name = {name: (kind, body) for kind, name, body in blocks}
    ident = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")

    keep = set()
    frontier = [r.strip() for r in args.roots.split(",") if r.strip()]
    for r in frontier:
        if r not in by_name:
            raise SystemExit(f"root {r} not found in {args.proto}")
    while frontier:
        name = frontier.pop()
        if name in keep:
            continue
        keep.add(name)
        _, body = by_name[name]
        for tok in set(ident.findall(body)):
            if tok in by_name and tok not in keep:
                frontier.append(tok)

    kept_blocks = [(kind, name, body) for kind, name, body in blocks if name in keep]
    with open(args.out, "w") as f:
        f.write('syntax = "proto3";\n')
        f.write(f"package {args.package};\n\n")
        for _, _, body in kept_blocks:
            f.write(body)
            f.write("\n\n")

    msgs = sum(1 for k, _, _ in kept_blocks if k == "message")
    enums = sum(1 for k, _, _ in kept_blocks if k == "enum")
    print(f"pruned closure: {msgs} messages, {enums} enums (from {len(blocks)} definitions)")


if __name__ == "__main__":
    main()
