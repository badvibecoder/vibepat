# vibepat

Pattern-based extraction and safe rewriting for logs, config files, and hardware
topology.

`vibepat` reads text, splits it into logical stanzas, and pulls out the values
that matter — IP addresses, PCI IDs, MAC addresses, NUMA nodes, error lines. It
emits newline-delimited JSON you can pipe into `jq`, an LLM, or a monitoring
pipeline. When you need to change something, it shows you a diff and backs the
file up before it writes.

```
                            vibepat get all [bdf, link_downgrade] require all
   lspci -vv ──┐                                                      │
   ip -d a  ───┼──►  chunk into stanzas  ──►  match tokens  ──►  NDJSON
   syslog   ───┘                                                      │
   config   ───┘                                            one object per line
```

## When to use it

Five jobs it is built for. Each example uses real output — try them against
`cmd/vibepat/testdata/lspci.txt`, which ships in the repo.

---

### 1. Find degraded hardware before it becomes a ticket

A PCIe link that trained slower than it is capable of is the signature of a bad
riser, a mis-seated card, or a slot wired narrower than the card. `lspci` buries
that in hundreds of lines.

```sh
$ vibepat get all [bdf, link_downgrade] require all cmd/vibepat/testdata/lspci.txt
{"matched_tokens":{"bdf":["00:01.2"],"link_downgrade":["link downgraded: speed 2.5GT/s of 32GT/s"]},...}
{"matched_tokens":{"bdf":["00:02.1"],"link_downgrade":["link downgraded: speed 2.5GT/s of 32GT/s"]},...}
```

**Why `require all`:** without it you get all 36 devices, because every device has
a `bdf`. `require all` demands that *every* target match, so only genuinely
degraded links survive. That is the difference between 36 results and 5.

> **Bridges versus endpoints.** Root ports idle at low link speed when nothing is
> downstream, so a flagged *bridge* is usually benign. A flagged **endpoint** — an
> NVMe drive or a NIC — is the one worth chasing. See
> [docs/tokens.md](docs/tokens.md#pcie-link-downgrades).

---

### 2. Pull addresses out of interface and config dumps

```sh
$ vibepat get all [ip, mac] /tmp/links.txt
{"matched_tokens":{"ip":["127.0.0.1"]},"stanza":{"lines":["1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536",...
{"matched_tokens":{"ip":["10.0.0.115","10.0.0.255","fe80::29b3:552e:8bcf:72a5"],"mac":["38:05:25:38:e7:01","ff:ff:ff:ff:ff:ff"]},...
```

Every value is preserved in an array — nothing is joined or truncated. Add
`with context N` to carry the surrounding lines along with each match:

```sh
$ vibepat get all [ip] with context 1 /tmp/links.txt
{"matched_tokens":{"ip":["10.0.0.115","10.0.0.255","fe80::29b3:552e:8bcf:72a5"]},"context_lines":["    inet 127.0.0.1/8 scope host lo"],...
```

Each line of output is a complete JSON object, so a JSON processor composes
directly when one is available:

```sh
$ vibepat get all [ip] /tmp/links.txt | jq -r '.matched_tokens.ip[]'
127.0.0.1
10.0.0.115
10.0.0.255
fe80::29b3:552e:8bcf:72a5
```

**Which form to use.** The grammar is the primary interface: `get all [ip, mac]`
reads as the question you are asking, and it is the only form that works with
scopes (`first 3`), `require all`, and `with context`. The `--tokens ip,mac` flag
is the shorthand for token selection alone, and is equivalent for that purpose.

---

### 3. Watch a live log for errors, without buffering

```sh
$ tail -f /var/log/syslog | vibepat get all [error]
{"matched_tokens":{"error":["ERROR"]},"stanza":{"lines":["2026-02-10T09:14:02Z [ERROR] nvme0n1: I/O error"],...
```

Output appears the moment a matching line arrives — it is NDJSON, emitted per
match, not a JSON array assembled at exit. That is what makes `tail -f` and
`head -1` work:

```sh
$ journalctl -f | vibepat get all [error] | head -1
```

`first N` and `stanza N` also stop reading as soon as they have their answer, so
they terminate against an endless stream.

**Why not `grep`:** `grep` gives you the raw line. This gives you structured
fields — the error keyword *and* whatever else the same stanza contains — in a
shape a program can consume.

---

### 4. Rewrite every host in a subnet, safely

Change the network portion of every address in `10.0.1.0/24` while leaving the
host portion and every other subnet untouched:

```sh
$ cat host.conf
server_ip=10.0.1.7
gateway=10.9.9.7
backup_dns=10.0.1.53

$ vibepat replace ip ['10.0.1.X'] with ['10.50.1.X'] dryrun host.conf
@@ line 1 @@
-    1  server_ip=10.0.1.7
+    1  server_ip=10.50.1.7

@@ line 3 @@
-    3  backup_dns=10.0.1.53
+    3  backup_dns=10.50.1.53
```

The file is unchanged and no backup exists — that was `dryrun`. Drop `dryrun` to
be prompted, or use `yolo` to write immediately:

```sh
$ vibepat replace ip ['10.0.1.X'] with ['10.50.1.X'] host.conf
Commit 2 changes? [y/N] y

$ cat host.conf
server_ip=10.50.1.7
gateway=10.9.9.7          # untouched: outside the subnet
backup_dns=10.50.1.53

$ cat host.conf.bak       # byte-identical to the original
server_ip=10.0.1.7
gateway=10.9.9.7
backup_dns=10.0.1.53
```

**Why not `sed -i`:** `sed -i` has no dry run, no backup, no diff, and no
validation. It will happily rewrite a file that changed underneath it. Here the
plan is validated against the file on disk before you are even asked to approve
it, the write is atomic (temp file + rename, so a crash cannot leave a
half-written config), permissions are preserved, and the backup is written once
and never clobbered by a later run.

---

### 5. Track down a timestamp masquerading as a MAC

Log timestamps and MAC addresses are the same shape, which is exactly why naive
matches fill a report with noise:

```sh
$ printf 'Timestamp 12:00:14:ab:12:a5 and MAC 38:00:14:ab:12:a5.\n' | vibepat --tokens mac
{"matched_tokens":{"mac":["38:00:14:ab:12:a5"]},...}
```

`12:00:14` reads as a time-of-day and is dropped; `38` is not a valid hour, so it
is kept. The same rigidity applies to the other tokens:

```sh
$ printf 'Check 999.888.777.666 and 10.0.0.256 before pinging 10.0.0.1.\n' | vibepat --tokens ip
{"matched_tokens":{"ip":["10.0.0.1"]},...}
```

Address validation is delegated to Go's `net/netip`, so octet boundaries are
enforced mathematically rather than by regex. See
[docs/tokens.md](docs/tokens.md#heuristics-and-their-limits) for the documented
cost of each heuristic.

---

## Built-in reference

Full offline documentation ships inside the binary. No network, no manual, no
`man` page, and no `--help` round trip to a website:

```sh
vibepat help              # START HERE: a worked example you can run in a minute
vibepat help overview     # the mental model: how text becomes stanzas
vibepat help grammar      # syntax, execution order, precedence, actions, scopes
vibepat help tokens       # every token, its validation, and custom tokens
vibepat help modifiers    # flags, write modes, and the safety guarantees
vibepat help examples     # datacenter workflows, as copyable recipes
vibepat help all          # the complete manual, every topic in order
vibepat --help            # index of topics
```

`help` is a teaching page, not a wall of reference: it walks one sample file
through `get`, `keep`, `drop`, and a `dryrun` rewrite, and explains each field of
the JSON as it appears. Every command on it is exercised by the test suite. When
you already know what you want, `help all` is the full 762-line manual.

`help` pages through `$PAGER` (falling back to `less -R`, then `more`) when
stdout is a terminal, and prints clean plaintext when piped, so
`vibepat help tokens | grep netip` works. Inside `vibepat -i`, `help` and
`help <topic>` open the same reference without leaving the session, and `<TAB>`
completes topic names.

## Install

```sh
# Prebuilt static binaries: linux/amd64 and windows/amd64, with checksums
make release

# Or build just for this host
make build          # ./vibepat
```

With Go 1.27 or newer, install straight from the module:

```sh
go install github.com/badvibecoder/vibepat/cmd/vibepat@latest
```

Single static binary, no runtime dependencies. See
[docs/development.md](docs/development.md) for the release process.

## Usage

```
vibepat [flags] [ACTION] [SCOPE] [TARGETS] [MODIFIERS] [FILE]
```

Every component is optional except that `replace` needs a `with` clause. The
action defaults to `get`, the scope to `all`, and input defaults to stdin.

```sh
lspci -vvv | vibepat get all [bdf, numa, link_downgrade]
vibepat keep first 3 [error] ./syslog
vibepat --mode header get all [bdf] ./lspci.txt
```

Flags may be written anywhere — before, after, or between grammar terms.

| Flag | Default | Meaning |
| :--- | :--- | :--- |
| `--mode M` | `auto` | Chunking heuristic: `auto`, `stream`, `header`, `indent`. |
| `--sample N` | `100` | Leading lines inspected when `--mode` is `auto`. |
| `--tokens A,B` | all | Tokens to match. Supplying this enables filter mode. |
| `--context N` | `0` | Lines of *preceding* context to embed in each match. The grammar's `with context N` overrides it. |
| `--file PATH` | | Input file, overriding positional path inference. |
| `--exec M` | `default` | Write mode: `default`, `yolo`, `spot`. |
| `--spot N` | `5` | Diffs shown in spot mode. |
| `--dryrun` | `false` | Compute the diff, never write. |
| `--no-color` | `false` | Disable ANSI colour. |
| `-i` | `false` | Interactive REPL. Requires a terminal. |
| `--no-custom-tokens` | `false` | Ignore `~/.vibepat/custom.yaml`. |
| `-h`, `--help` | | Print the help index and exit. See [Built-in reference](#built-in-reference). |
| `--version` | | Print version and exit. |

### Reference

| Topic | Document |
| :--- | :--- |
| Every action, scope, and modifier | [docs/grammar.md](docs/grammar.md) |
| Every token, its validation, and its limits | [docs/tokens.md](docs/tokens.md) |
| The JSON contract and streaming behaviour | [docs/output.md](docs/output.md) |
| Backups, atomic writes, confirmation | [docs/safety.md](docs/safety.md) |
| Layout, build, release, testing | [docs/development.md](docs/development.md) |

## Tokens at a glance

| Token | Matches |
| :--- | :--- |
| `ip` | IPv4 and IPv6, octet-validated |
| `mac` | MAC address, colon / dash / Cisco dotted |
| `bdf` | PCI Bus:Device.Function |
| `numa` | NUMA node number |
| `error` | Structured error signal only (`[ERROR]`, `ERR:`, `failed`) |
| `error_loose` | Structured **plus** any bare error word |
| `link_downgrade` | PCIe link below its capability |

Custom tokens go in `~/.vibepat/custom.yaml`; see
[docs/tokens.md](docs/tokens.md#custom-tokens).

## Verified against real data

The test suite includes a real root `lspci -vv` capture
(`cmd/vibepat/testdata/lspci.txt`, 1462 lines, 36 devices), not hand-written
approximations. It has repeatedly caught bugs that invented fixtures hid — most
notably a mis-calibrated auto-detection threshold, because only 2.5% of real
`lspci` lines are headers. See
[docs/development.md](docs/development.md#testing).

```sh
make test    # 319 tests
```

## Layout

```
cmd/vibepat/          the command: package main, CLI source and tests
  testdata/           fixtures, including the real lspci capture
internal/registry/    the semantic token registry, a separate package
docs/                 documentation
scripts/              build tooling
Makefile              build, test, and release entry points
RELEASE_NOTES.md      notes for the current release
```

The layout follows the usual Go convention: command code under `cmd/`, packages
not meant for import under `internal/`, and the module file at the root.

## License

Dependency licences and attribution:
[docs/THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md).
