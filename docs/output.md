# Output contract

Read-only runs emit **NDJSON**: one JSON object per line, written as soon as each
stanza matches. Progress and errors go to stderr, never into the JSON.

```sh
$ vibepat --tokens bdf --mode header lspci.txt | head -2
{"matched_tokens":{"bdf":["00:00.0"]},"context_lines":[],"stanza_index":1,...}
{"matched_tokens":{"bdf":["00:00.2"]},"context_lines":[],"stanza_index":2,...}
```

(Elided with `...` above for width. The tool emits the full object on one line.)

NDJSON rather than a single JSON array is deliberate. An array cannot be emitted
until the last match is known, which breaks `tail -f`, `head`, and every other
streaming consumer:

```sh
tail -f /var/log/syslog | vibepat get all [error]
journalctl -f | vibepat get all [ip] | head -1
```

Memory is bounded accordingly: only the preceding-context ring buffer is retained,
so a read-only run over an unbounded stream stays flat. The scopes preserve this
— `first N` and `stanza N` stop reading as soon as they have their answer, and
`last N` retains exactly N results.

A run that produces no matches writes **nothing at all**, rather than an empty
array.

## Record shape

```json
{
  "matched_tokens": { "ip": ["10.0.0.1", "10.0.0.2"] },
  "context_lines": [],
  "stanza_index": 1,
  "line_number": 1,
  "position_kind": "stanza",
  "stanza": {
    "lines": ["addr 10.0.0.1 x 10.0.0.2"],
    "raw_text": "addr 10.0.0.1 x 10.0.0.2",
    "boundary_type": "stream"
  },
  "mutations": null
}
```

| Field | Meaning |
| :--- | :--- |
| `matched_tokens` | Token name → **array** of every value found, in order of appearance. |
| `context_lines` | Preceding lines, oldest first. Never includes the matched stanza. |
| `stanza_index` | 1-based position of the stanza in the input. |
| `line_number` | 1-based input line where the stanza began. |
| `position_kind` | `"line"` or `"stanza"`; says which positional field is authoritative. |
| `stanza.lines` | The stanza's lines, newlines stripped. |
| `stanza.raw_text` | The same lines rejoined with `\n`; what matchers run against. |
| `stanza.boundary_type` | `stream`, `header`, or `indent` — how it was delimited. |
| `mutations` | Always `null` in read-only output. |

## Guarantees consumers may rely on

- **Every key is always present.** Fields that do not apply carry `""`, `0`, or
  `null`; they are never omitted. `matched_tokens` and `context_lines` are `{}`
  and `[]`, never `null`, so neither needs a nil check.
- **`matched_tokens` is `map[string][]string`.** Every token maps to an array,
  even when there is exactly one value, so consumers never handle two shapes. A
  stanza with four IPs reports all four; nothing is joined or truncated.
- **A token with no matches is absent**, rather than present-and-empty. In a
  multi-target query, only the targets that actually matched appear.
- **Chunking is lossless.** Every non-blank input line appears exactly once, in
  order, across the emitted stanzas. Only blank separator lines are dropped.
- **Key order within an object is not stable** (Go map iteration is randomized).
  Select by name; do not depend on order.
- **Empty input yields no output**, not `[]`.

## Write runs

`replace` is the exception to NDJSON: it emits a single JSON execution summary
object, because a mutation plan must be validated against the whole original file
before anything is written.

```json
{
  "status": "applied",
  "source_path": "/etc/ssh/sshd_config",
  "backup_path": "/etc/ssh/sshd_config.bak",
  "applied": 2,
  "total": 2,
  "sampled": 0,
  "mutations": [
    { "line_number": 2, "original_text": "...", "modified_text": "...", "context_lines": null }
  ],
  "error": ""
}
```

`status` is one of `applied`, `no_changes`, `aborted`, `dryrun`, or `error`.

## Stream separation

`stdout` is reserved for machine-readable output. The human-facing diff always
goes to `stderr`, in every mode, so nothing can corrupt the JSON stream:

```sh
# The diff is visible on the terminal; stdout stays parseable.
vibepat replace ip ['10.0.1.X'] with ['10.50.1.X'] ./config >result.json
```
