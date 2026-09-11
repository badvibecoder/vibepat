#!/usr/bin/env python3
"""Print one field from every NDJSON object vibepat emits.

    vibepat get all [ip] w.txt | python3 field.py matched_tokens.ip

`matched_tokens.ip` walks into the object and prints each value on its own line.
Lists are flattened, so one address per line comes out ready for `sort -u`.
Missing fields are skipped rather than printed as errors, which keeps pipelines
quiet.
"""
import json
import sys

if len(sys.argv) != 2:
    sys.exit("usage: ... | python3 field.py matched_tokens.ip")

path = sys.argv[1].split(".")
for line in sys.stdin:
    if not line.strip():
        continue
    value = json.loads(line)
    for key in path:
        value = value.get(key) if isinstance(value, dict) else None
    if value is None:
        continue
    if isinstance(value, list):
        for item in value:
            print(item)
    else:
        print(value)
