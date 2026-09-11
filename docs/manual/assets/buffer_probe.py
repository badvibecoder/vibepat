#!/usr/bin/env python3
"""When does vibepat's output actually reach the reader?

The producer emits one matching line immediately, waits two seconds, then emits
a second one. If vibepat wrote each match as it was found, the first line would
arrive at about +0.00s. This probe measures the truth on a pipe.
"""
import subprocess
import time

producer = ("( printf 'addr 10.0.0.1\\n'; sleep 2; printf 'addr 10.0.0.2\\n' ) "
            "| vibepat get all [ip]")

proc = subprocess.Popen(["bash", "-c", producer], stdout=subprocess.PIPE,
                        text=True, bufsize=1)
start = time.time()
for line in proc.stdout:
    print(f"match reached the reader at +{time.time() - start:.2f}s")
