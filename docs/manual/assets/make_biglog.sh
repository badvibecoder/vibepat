#!/usr/bin/env bash
# Build a large log to time the examples in Chapter 7 against.
# 200,000 lines, about 11 MB, with one ERROR line every 1,000 lines.
set -eu
cd "$(dirname "$0")"
{ for i in $(seq 1 200000); do
    case $((i % 1000)) in
      0) printf '2026-02-10T09:14:02Z node app[%d]: [ERROR] failure on 10.0.%d.%d\n' "$i" "$((i%256))" "$((i%256))" ;;
      *) printf '2026-02-10T09:14:02Z node app[%d]: ok 10.0.%d.%d\n' "$i" "$((i%256))" "$((i%256))" ;;
    esac
  done; } > big.log
ls -lh big.log
