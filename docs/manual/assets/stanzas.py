#!/usr/bin/env python3
"""Print each stanza the way vibepat matched it: a heading, then its lines.

    vibepat --mode header get all app.ini | python3 stanzas.py

This is the quickest way to see what chunking actually did, which matters
because the same file can produce a different number of stanzas under each mode.
"""
import json
import sys

for line in sys.stdin:
    if not line.strip():
        continue
    obj = json.loads(line)
    stanza = obj["stanza"]
    print(f"--- stanza {obj['stanza_index']} "
          f"(line {obj['line_number']}, {stanza['boundary_type']})")
    for text in stanza["lines"]:
        print("   ", text)
