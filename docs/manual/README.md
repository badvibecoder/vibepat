# vibepat Training Manual

A practical, plain-English training manual for **vibepat 0.1.0** — a
pattern-based extraction and safe-rewriting tool for logs, config files, and
hardware topology.

Written for junior engineers and low-to-medium technical readers: no regular
expressions required, every command written out in full, every result explained
field by field.

This folder is self-contained. Inside the vibepat repository it belongs at
`docs/manual/`, beside the reference pages in `docs/`.

## Read it

**[vibepat-training-manual.md](vibepat-training-manual.md)** — about a 90-minute
read, or a reference you can jump around in.

| Chapter | What you can do afterwards |
| :--- | :--- |
| 1. Getting vibepat | Install it, prove it runs, and read the built-in manual offline. |
| 2. Your first command | Read the JSON result of a query and tell stdout from stderr. |
| 3. Mind your tokens | Pick the right token, and know what each one refuses to match. |
| 4. Sharpening the question | Use actions, scopes, `require all`, and context correctly. |
| 5. Chunks: why stanzas matter | Choose a chunking mode and understand why counts change. |
| 6. Changing files without fear | Rewrite a file safely — and avoid the one rewrite that corrupts data. |
| 7. Troubleshooting logs | Get from a large log to the first real failure. |
| 8. Network troubleshooting | Inventoried addresses, audit a config, redact a capture. |
| 9. Hardware health | Find degraded PCIe links and tell the real findings from idle bridges. |
| 10. Advanced use | Write custom tokens, script it, and know the limits. |
| Appendices | Cheat sheet, exit codes, error messages, JSON contract, glossary. |

## What is in this folder

```
vibepat-training-manual.md   the manual
img/                         figures (referenced by the manual)
assets/                      every sample file the manual uses, and the helpers
  field.py  cols.py  ndjson.py  stanzas.py   JSON output helpers
  buffer_probe.py                            output-buffering demonstration
  make_biglog.sh                             builds the 11 MB log used in Ch 7
```

Commands in the manual assume you are in `assets/`:

```sh
cd assets
vibepat get all [ip] w.txt
```

## The figures

`img/` holds tight, annotated screenshots. Each one shows only the lines that
matter, with numbered callouts explained in the caption and the prose. They are
**not** hand-drawn: every figure is a render of captured command output, and a
callout locates its target by searching for the exact text it annotates, so a box
cannot point at the wrong words.

## Running the examples

```sh
cd assets
vibepat --version
vibepat get all [ip] w.txt
```

Everything the manual needs is in `assets/`; commands that write make a copy
first, so the originals stay pristine. Four helpers turn vibepat's JSON into
something easier to read on a terminal:

```sh
vibepat get all [ip] w.txt | python3 field.py matched_tokens.ip
vibepat get all [ip] w.txt | head -1 | python3 ndjson.py
```

`today.log` is dated for the day it was created; Chapter 4 shows the two-line
command that makes a fresh one.

## Review

This manual was reviewed by three independent passes before release: one on
content, audience, and uniformity; one that executed every command in the book
(165 command-level checks); and one that inspected all 53 figures and replayed
the annotation geometry of all 123 callouts. Every finding was re-verified
against the binary before being applied or rejected.

## Provenance and honesty

This manual was written against **vibepat 0.1.0** and tested on a Linux host. The
behavior described was measured, not assumed: where the program and its own
built-in reference disagree, the manual says so in plain terms and tells you the
safe way to work. The twelve "edges" in Chapter 10 are the condensed list; each
chapter's fine print has the detail.

Real data used by the manual:

- `assets/links.txt` — `ip -d a` captured on the authoring host.
- `assets/lspci-mini.txt`, `assets/lspci-nonroot.txt` — unprivileged `lspci`
  from the authoring host.
- `assets/lspci-root.txt` — a real **root** `lspci -vv` capture (36 devices,
  5 degraded links). This is the project's own test fixture, referenced by its
  README; reading link state requires root, so it could not be captured
  unprivileged here.

All other sample files are synthetic, written to be safe to experiment on and to
demonstrate a specific behaviour.
