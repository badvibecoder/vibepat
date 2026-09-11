# Grammar reference

`vibepat` accepts `[ACTION] [SCOPE] [TARGETS] [MODIFIERS]`. Every component is
optional; the action defaults to `get` and the scope to `all`.

| Component | Values |
| :--- | :--- |
| **ACTION** | `get` (default), `keep`, `drop`, `replace` |
| **SCOPE** | `all`, `first N`, `last N`, `today`, `stanza N` |
| **TARGETS** | `[ip]`, `[bdf]`, `[mac]`, `[numa]`, `[error]`, `['literal']`, or a comma list |
| **MODIFIERS** | `with ['X']`, `with context N`, `require all`, `yolo` / `force`, `spot N`, `dryrun`, `no-color` |

The grammar is written as **raw positional arguments**, with an optional file path
at the end:

```sh
lspci -vvv | vibepat get all [bdf, numa, link_downgrade]
vibepat keep first 3 [error] ./syslog
vibepat replace ip ['10.0.1.X'] with ['10.50.1.X'] ./config
```

**Flags may be written anywhere** — before, after, or interleaved with the
grammar:

```sh
vibepat get all [bdf] --mode header ./lspci.txt
vibepat --mode header get all [bdf] ./lspci.txt   # equivalent
```

The trailing argument is treated as the input file when it names a readable
regular file; otherwise every argument belongs to the grammar and input comes
from stdin. When that inference is unwelcome, `--file` states it explicitly:

```sh
vibepat --file ./config replace ip ['10.0.1.X'] with ['10.50.1.X']
```

## Actions

| Action | Behaviour |
| :--- | :--- |
| `get` | Report matching stanzas. Read-only. |
| `keep` | Report matching stanzas. Read-only, identical output to `get`. |
| `drop` | Report the **complement**: the stanzas that did *not* match. Read-only. |
| `replace` | Rewrite matching text. The only action that can write to disk. |

`keep` and `drop` never write and never create a backup.

## Scopes

`first N` and `last N` bound the number of **matches reported**, not the size of
the input window. So `get first 3 [ip]` returns three matching stanzas even when
it must read past non-matching lines to find them.

| Scope | Meaning |
| :--- | :--- |
| `all` | Every stanza. The default. |
| `first N` | The first N matches. Reading stops once N are found. |
| `last N` | The last N matches. Only N results are retained in memory. |
| `today` | Stanzas whose text contains today's date in a common log format. |
| `stanza N` | One stanza by 1-based index. |

`today` is a **text heuristic**, not a date parser. It recognises `2026-02-10`,
`Feb 10`, `Feb 02`, `02/10/2026`, `10/02/2026`, `2026/02/10`, and
`Mon Feb 2`.

## How multiple targets combine

By default targets are **alternatives**: a stanza matches if *any* named token is
present. That is right for exploration.

It is wrong for questions of the form "which devices are degraded?", because
every device has a `bdf`, so the alternative reading reports the whole machine.
Adding `require all` switches to **conjunctive** matching:

```sh
# every device -- bdf matches them all
vibepat get all [bdf, link_downgrade] ./lspci.txt

# only devices whose PCIe link is degraded
vibepat get all [bdf, link_downgrade] require all ./lspci.txt
```

In a multi-target query, only the targets that actually matched appear in
`matched_tokens`. A device that is not degraded simply has no `link_downgrade`
key rather than a null one.

## Replacement patterns

An IP target understands the fourth-octet wildcard and CIDR prefixes. Both
rewrite the network portion while preserving the host portion:

```sh
vibepat replace ip ['10.0.1.X'] with ['10.50.1.X'] yolo ./config
vibepat replace ip ['10.0.1.0/24'] with ['10.50.1.X'] yolo ./config
```

Both produce `10.0.1.7 -> 10.50.1.7` and leave `10.9.9.7` untouched.

- `X` is only valid as the **fourth octet**. Using it elsewhere is a parse error
  rather than a silent non-match.
- A target that is neither a registered token nor an IP-shaped value is matched
  as an exact literal string.
- `replace mac with ['REDACTED']` — a bare token name with no pattern — replaces
  every value of that token.
- An empty replacement (`with ['']`) deletes the matched text. It is distinct
  from omitting the clause, which is a syntax error.

## Modifiers

| Modifier | Effect |
| :--- | :--- |
| `with ['X']` | The replacement value. Required by `replace`. |
| `with context N` | Embed the N lines *preceding* each match. Read-only queries only. |
| `require all` | Every target must match, not just one. |
| `yolo` / `force` | Write without prompting. |
| `spot N` | Show N random diffs, then prompt once for the whole batch. |
| `dryrun` | Compute the diff and summary, never write. |
| `no-color` | Plain diff output. |

## Context

`with context N` and the `--context N` flag both embed the lines that precede a
match, oldest first. Neither ever includes the matched stanza itself.

```sh
vibepat get all [ip] with context 3 ./links.txt
vibepat --context 3 get all [ip] ./links.txt     # equivalent
```

The grammar form overrides the flag when both are present, so a script can set a
default with `--context` and a single command can raise it.

Context is available on read-only queries. `replace` does not accept it, because
a rewrite report describes changes rather than surrounding text.

## Interactive mode

```sh
vibepat -i ./config
```

```
vibepat> get all [bdf, numa]
vibepat> replace ip ['10.0.1.X'] with ['10.50.1.X']
vibepat> replace ip ['10.0.1.X'] with ['10.50.1.X'] yolo
```

`<TAB>` completes contextually, with descriptions:

| What you have typed | What `<TAB>` offers |
| :--- | :--- |
| *(empty)* | the actions, `get` / `keep` / `drop` / `replace` |
| an action | the semantic tokens **and** the scopes |
| a target | the modifiers, `with` / `require` / `yolo` / `spot` / `dryrun` |
| `with` | nothing — the replacement is free text |

Token suggestions are pulled live from the registry, so custom tokens from
`custom.yaml` appear automatically. REPL meta commands: `help`, `tokens`,
`lines`, `exit`.

Interactive mode requires a **real terminal** on stdin. Piped input is refused
with a pointer to the positional grammar rather than being started and failing.
