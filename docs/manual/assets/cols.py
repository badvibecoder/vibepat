#!/usr/bin/env python3
"""Pick columns out of each NDJSON object — `cut`, but for vibepat's output.

    vibepat get all [bdf, link_downgrade] require all lspci.txt \
      | python3 cols.py matched_tokens.bdf matched_tokens.link_downgrade

Each argument is a dotted path into the object. A numeric segment indexes a list,
so `stanza.lines.0` is the first line of the stanza. List values are joined with
commas. Objects with a missing field print nothing for that column, so the
output stays aligned and readable.
"""
import json
import sys

if len(sys.argv) < 2:
    sys.exit("usage: ... | python3 cols.py matched_tokens.bdf matched_tokens.link_downgrade")

columns = sys.argv[1:]
for line in sys.stdin:
    if not line.strip():
        continue
    obj = json.loads(line)
    values = []
    for column in columns:
        value = obj
        for key in column.split("."):
            if isinstance(value, list) and key.isdigit():
                index = int(key)
                value = value[index] if index < len(value) else None
            else:
                value = value.get(key) if isinstance(value, dict) else None
        if isinstance(value, list):
            value = ",".join(str(v) for v in value)
        values.append("" if value is None else str(value))
    print("  ".join(values))
