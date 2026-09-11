#!/usr/bin/env python3
"""Pretty-print each newline-delimited JSON object from vibepat.

    vibepat get all [ip] w.txt | python3 ndjson.py

Handy because `python3 -m json.tool` expects a single JSON document, and
vibepat emits one complete object per line.
"""
import json
import sys

for line in sys.stdin:
    if line.strip():
        print(json.dumps(json.loads(line), indent=2))
