#!/usr/bin/env python3
"""Field-level thinning of a proto closure.

Keeps only fields whose protoc-gen-go getter is used somewhere in a set of Go
source files; deletes every other field. Field NUMBERS are preserved, so a
deleted field simply becomes an unknown field on the wire — skipped during
decode (with DiscardUnknown) instead of allocating its (possibly large)
subtree. This is the static, schema-level equivalent of lazy decoding.

Conservatism knobs (all err toward UNDER-thinning, so the measured win is a
lower bound):
  * Only depth-1 (direct member) field statements are considered. Fields inside
    a nested oneof/message/enum block are left intact.
  * Getter-name matching is global and collision-tolerant (a field is kept if
    ANY message's getter of that name is used).
"""

import argparse
import re


def parse_blocks(text):
    blocks = []
    i = 0
    pat = re.compile(r"^(message|enum)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{", re.M)
    while True:
        m = pat.search(text, i)
        if not m:
            break
        depth = 0
        j = m.end() - 1
        while j < len(text):
            if text[j] == "{":
                depth += 1
            elif text[j] == "}":
                depth -= 1
                if depth == 0:
                    break
            j += 1
        blocks.append((m.group(1), m.group(2), text[m.start():j + 1]))
        i = j + 1
    return blocks


def go_camel(field):
    """protoc-gen-go GoCamelCase (simplified): capitalize each _-segment."""
    out = []
    cap = True
    for ch in field:
        if ch == "_":
            cap = True
        elif cap and ch.islower():
            out.append(ch.upper())
            cap = False
        else:
            out.append(ch)
            cap = False
    return "".join(out)


def used_getters(go_files):
    getters = set()
    for path in go_files:
        with open(path) as f:
            for m in re.finditer(r"\.Get([A-Z][A-Za-z0-9_]*)\(\)", f.read()):
                getters.add(m.group(1))
    return getters


# A depth-1 singular/repeated field statement: [repeated] Type name = N [opts];
FIELD_RE = re.compile(
    r"^(\s*)(repeated\s+|optional\s+)?[A-Za-z_][A-Za-z0-9_.]*\s+([a-z_][A-Za-z0-9_]*)\s*=\s*\d+\s*(\[[^\]]*\])?\s*;\s*$"
)


def thin_message_body(body, used):
    """body includes 'message Name { ... }'. Drop depth-1 field statements whose
    getter is unused; leave nested blocks and non-field statements intact."""
    open_idx = body.index("{")
    header, inner, footer = body[:open_idx + 1], body[open_idx + 1:body.rindex("}")], body[body.rindex("}"):]
    out_lines = []
    depth = 0
    removed = kept = 0
    for line in inner.splitlines(keepends=True):
        stripped = line.strip()
        at_depth1 = depth == 0
        # A field statement only counts at depth 0 (direct member) and must not
        # itself open a block.
        if at_depth1 and "{" not in line:
            m = FIELD_RE.match(line)
            if m and stripped.split()[0] not in ("oneof", "message", "enum", "reserved", "option"):
                field = m.group(3)
                if go_camel(field) in used:
                    out_lines.append(line)
                    kept += 1
                else:
                    removed += 1
                depth += line.count("{") - line.count("}")
                continue
        out_lines.append(line)
        depth += line.count("{") - line.count("}")
    return header + "".join(out_lines) + footer, removed, kept


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--proto", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--roots", required=True)
    ap.add_argument("--package", required=True)
    ap.add_argument("--go-src", required=True, nargs="+", help="Go files defining the read set")
    args = ap.parse_args()

    text = open(args.proto).read()
    blocks = parse_blocks(text)
    by_name = {name: (kind, body) for kind, name, body in blocks}
    ident = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")

    # Transitive closure from roots (over the FULL bodies, so we don't lose a
    # message that's only referenced by a to-be-kept field).
    keep = set()
    frontier = [r.strip() for r in args.roots.split(",") if r.strip()]
    while frontier:
        name = frontier.pop()
        if name in keep:
            continue
        keep.add(name)
        for tok in set(ident.findall(by_name[name][1])):
            if tok in by_name and tok not in keep:
                frontier.append(tok)

    used = used_getters(args.go_src)
    total_removed = total_kept = 0
    out_blocks = []
    for kind, name, body in blocks:
        if name not in keep:
            continue
        if kind == "message":
            body, r, k = thin_message_body(body, used)
            total_removed += r
            total_kept += k
        out_blocks.append(body)

    with open(args.out, "w") as f:
        f.write('syntax = "proto3";\n')
        f.write(f"package {args.package};\n\n")
        for body in out_blocks:
            f.write(body + "\n\n")

    print(f"thinned: kept {total_kept} depth-1 fields, removed {total_removed} "
          f"({100*total_removed/max(1,total_kept+total_removed):.0f}% of depth-1 fields) "
          f"across {sum(1 for k,_,_ in blocks if k=='message' and _ in keep) if False else len(out_blocks)} definitions")


if __name__ == "__main__":
    main()
