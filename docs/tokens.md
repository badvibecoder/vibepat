# Semantic tokens

Every stanza is scanned for named tokens. Matchers live in a process-wide
registry and are looked up case-insensitively.

| Token | Matches | Examples |
| :--- | :--- | :--- |
| `ip` | IPv4 and IPv6 addresses, octet-validated | `10.0.0.1`, `2001:db8::1` |
| `mac` | MAC address, colon / dash / Cisco dotted | `38:00:14:ab:12:a5`, `3800.14ab.12a5` |
| `bdf` | PCI Bus:Device.Function | `0000:41:00.0`, `41:00.0` |
| `numa` | NUMA node number | `NUMA node: 0` → `0` |
| `error` | **Structured** error signal only | `[ERROR]`, `ERR:`, `failed`, `critical` |
| `error_loose` | Structured **plus** any bare error word | `no errors found`, `an error occurred` |
| `link_downgrade` | PCIe link negotiated below its capability | `Speed 8GT/s` on a `16GT/s` link |

```sh
lspci -vv | vibepat --tokens bdf,numa
ip -d a  | vibepat --tokens ip,mac
```

With no `--tokens` flag, every registered token runs and **every stanza is
reported**. Supplying `--tokens` switches to **filter mode**: only stanzas
matching at least one named token are emitted, and an unknown name is a usage
error listing what is available.

## Validation is mathematical, not just regex

Address validation is delegated to `net/netip` rather than a hand-written octet
count. Regexes only *extract candidates*; the validator makes the accept/reject
decision. This keeps `999.888.777.666`, `10.0.0.256`, and `010.1.1.1` out while
still finding addresses embedded in punctuation:

```sh
$ printf 'bogus 999.888.777.666 but real 10.0.0.1\n' | vibepat --tokens ip --mode stream
{"matched_tokens":{"ip":["10.0.0.1"]},...}
```

Similarly, `bdf` requires a function nibble of 0-7, which is what stops a
timestamp like `12:00:00` from being read as a PCI address.

## Heuristics and their limits

Three tokens rely on heuristics rather than pure syntax. Each is documented with
its cost, and each has a test pinning the exact boundary.

### Timestamps versus MAC addresses

A log timestamp and a MAC address are frequently the same shape:

```
Timestamp 12:00:14:ab:12:a5 and MAC 38:00:14:ab:12:a5
```

`mac` rejects a colon-separated candidate whose first three octets read as a
valid time-of-day (HH ≤ 23, MM ≤ 59, SS ≤ 59), unless it is the all-zero null
address or the all-ff broadcast address — both legitimate and common:

```sh
$ printf 'Timestamp 12:00:14:ab:12:a5 and MAC 38:00:14:ab:12:a5.\n' | vibepat --tokens mac
{"matched_tokens":{"mac":["38:00:14:ab:12:a5"]},...}
```

**Known cost:** the ~0.1% of vendor prefixes whose three leading octets fall
inside 00-23:00-59:00-59 — `00:11:22:…` for example — are not reported, because
they are indistinguishable from a timestamp by shape alone. The trade-off is
deliberate: in log analysis a false timestamp match is constant noise, whereas a
missed MAC still appears whenever it is reported in dash or dotted form.

### PCIe link downgrades

`link_downgrade` compares a device's `LnkCap:` line against its `LnkSta:` line
and reports a link that trained at lower speed or narrower width than it is
capable of:

```
LnkCap: Port #0, Speed 32GT/s, Width x4
LnkSta: Speed 2.5GT/s, Width x4
```

```
link downgraded: speed 2.5GT/s of 32GT/s
```

Reading link state requires PCI configuration space, so a non-root `lspci -vv`
omits these lines and nothing is reported — no false positives.

**Interpretation warning:** root ports commonly idle at low link speed when
nothing is downstream, so a flagged *bridge* is usually benign. A flagged
**endpoint** (an NVMe drive or NIC) is the one worth investigating. Combining
with `bdf` and `require all` lists only the real cases.

### Strict versus loose errors

`error` matches only **structured** signal, because a bare `error` word floods
results on real logs. `error_loose` is the opt-in override that additionally
catches unstructured prose.

```sh
journalctl -b | vibepat --tokens error_loose
```

`error_loose` is deliberately broad and will also match identifier-shaped text
like `error_count`; use the strict token when that noise matters.

## Custom tokens

Define your own tokens in `~/.vibepat/custom.yaml`:

```yaml
tokens:
  sitename:
    regex: '\bsite\s+(\S+)'
    description: site code     # optional
  serial:
    regex: 'SN[:=]\s*(\w+)'
```

> **Quote regexes with single quotes.** In a YAML double-quoted scalar, `\s` is
> an invalid escape and the file fails to parse. Single-quoted YAML takes
> backslashes literally, which is what a regex author wants.

- When a pattern has a capture group, the **first group** is reported, so
  `regex: 'node(\d+)'` yields `0` rather than `node0`.
- Custom tokens are **additive**: a definition that would shadow a built-in token
  is rejected, as is an invalid regex or a malformed file. A broken custom file
  is a hard error rather than a silent skip.
- A **world-writable** `custom.yaml` is refused, since any local user could
  otherwise inject patterns into a root-run tool.
- Use `--no-custom-tokens` to ignore the file entirely.
