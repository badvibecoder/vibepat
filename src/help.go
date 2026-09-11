package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// This file is the built-in reference manual. It is the single source of truth
// for help content: the CLI, the --help flag, and the REPL all render from here,
// so the three can never drift apart.
//
// Content is deliberately kept as plain string constants rather than being
// generated from the parser and registry. The parser knows the shape of the
// grammar but not why a modifier exists or when to reach for it, and a reference
// manual that only restated the code would be useless. Tests assert that every
// token, action, scope, and modifier the code actually implements is mentioned
// here, which is what keeps the prose honest.

// helpTopics is the canonical ordering of topics, which is also the order they
// appear in the complete manual. "all" and "index" are assembled from these, so
// they cannot fall out of step with the individual pages.
var helpTopics = []string{"start", "overview", "grammar", "tokens", "modifiers", "examples"}

// helpTopicDescriptions documents each topic for the index page.
var helpTopicDescriptions = map[string]string{
	"start":     "START HERE: a worked example you can run in a minute",
	"overview":  "what vibepat is for, and the mental model in one page",
	"grammar":   "syntax, execution order, precedence, and every action and scope",
	"tokens":    "every built-in semantic token, its validation, and custom tokens",
	"modifiers": "execution flags, safety levels, and how they combine",
	"examples":  "real-world datacenter workflows, as copyable recipes",
}

// helpAliases maps alternative topic names onto canonical ones, so the common
// guesses work without being listed twice.
var helpAliases = map[string]string{
	"":         "start",
	"index":    "index",
	"topics":   "index",
	"contents": "index",
	"actions":  "grammar",
	"scopes":   "grammar",
	"flags":    "modifiers",
	"options":  "modifiers",
	"recipes":  "examples",
	"cookbook": "examples",
	"usage":    "overview",
	"intro":    "start",
	"readme":   "start",
	"quick":    "start",
	"tutorial": "start",
}

// helpPage holds one topic's content.
type helpPage struct {
	title string
	body  string
}

// helpPreamble is the shared header, so every entry point opens consistently.
const helpPreamble = `vibepat - pattern-based extraction and safe rewriting for logs, config, and
hardware topology

usage:
  vibepat [flags] [ACTION] [SCOPE] [TARGETS] [MODIFIERS] [FILE]

  The grammar is written as plain positional arguments, and the trailing
  argument is read as the input file when it names one. Flags may appear
  anywhere on the command line.

  New here? "vibepat help start" is a worked example you can try in a minute.
  The rest is reference, for when you know what you want.

  The shape of every command:

    ACTION  SCOPE  TARGETS            MODIFIERS
    get     all    [bdf, numa]        lspci.txt
    keep    first 3 [error]           --mode header syslog
    replace ip     ['10.0.1.X']  with ['10.50.1.X']  ./config
`

// helpBodies holds the canonical text for each topic.
var helpBodies = map[string]helpPage{
	"start": {title: "Start here", body: `START HERE

  A worked example. It takes about a minute, needs no hardware, and every command
  below is safe to run: none of them writes anything.

  Try it. Create a small file:

    printf 'addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\n' > /tmp/w.txt

  1. FIND THE ADDRESSES

    $ vibepat get all [ip] /tmp/w.txt
    {"matched_tokens":{"ip":["10.0.0.1"]},"context_lines":[],"stanza_index":1,...

  Two lines matched. You get one JSON object per line, printed the moment each
  match is found, which is why this works on a live stream.

  Reading the object, left to right:

    matched_tokens  what was found. Always an array, even for one value, so you
                    never have to handle two shapes.
    context_lines   the lines before the match. Empty here because neither
                    match has any. See step 4.
    stanza_index    which chunk of the input this is, counting from 1.
    line_number     the input line it started on.
    stanza.lines    the chunk itself, and boundary_type says how it was found.

  Notice that "plain-b" and "plain-c" did not match, so they were not printed.

  2. NARROW IT

    $ vibepat keep first 2 [ip] /tmp/w.txt
    ... two objects ...

  keep is the same as get, named for the intent of keeping the matches.
  "first 2" means the first two MATCHES, not the first two lines: if the file
  were 500 lines with the addresses near the end, this would still find them and
  stop reading as soon as it had two.

  Change "first 2" to "last 1" to get the last match instead, or drop the scope
  entirely for all of them.

  3. INVERT IT

    $ vibepat drop all [ip] /tmp/w.txt
    ... the objects for "plain-b" and "plain-c" ...

  drop reports the lines that did NOT match. This is how you prune noise out of
  a capture before reading it.

  4. SEE THE CHANGE BEFORE MAKING IT

    $ vibepat replace ip ['10.0.0.X'] with ['10.9.9.X'] dryrun /tmp/w.txt
    @@ line 1 @@
    -    1  addr 10.0.0.1
    +    1  addr 10.9.9.1

    @@ line 4 @@
    -    4  addr 10.0.0.4
    +    4  addr 10.9.9.4

  The X is the fourth octet: the first three are matched literally, and the
  captured octet is carried into the replacement. dryrun prints this diff and
  stops. /tmp/w.txt is unchanged and no .bak was created.

  To carry surrounding lines, add a context clause:

    $ vibepat keep all [ip] with context 1 /tmp/w.txt
    ... each match now has one preceding line in context_lines ...

  To actually rewrite, replace "dryrun":

    vibepat replace ip ['10.0.0.X'] with ['10.9.9.X'] /tmp/w.txt

  You are then shown the diff and prompted. Nothing is written until you answer
  y, and the original is saved to /tmp/w.txt.bak first. Add yolo in place of
  dryrun to skip the prompt in a script.

  WHERE TO GO NEXT

    vibepat help overview    the mental model: how text becomes stanzas
    vibepat help tokens      every token, and exactly what it will not match
    vibepat help modifiers   write modes, safety, and how to combine them
    vibepat help examples    datacenter workflows, ready to paste
    vibepat help all         everything, in order
`},

	"overview": {title: "Overview", body: `OVERVIEW

  vibepat reads text, splits it into logical stanzas, and pulls out the values
  that matter: IP addresses, PCI IDs, MAC addresses, NUMA nodes, error lines. It
  emits newline-delimited JSON you can pipe anywhere, and when you need to change
  something it shows a diff and backs the file up before writing.

  The mental model has three stages:

    chunk  ->  match  ->  act
    -----     -----      ---
    Split the  Find the   Report,
    input into named       filter,
    stanzas.   tokens.     or rewrite.

  CHUNKING
  Text is grouped into stanzas using visual heuristics, not a grammar, because
  the inputs vibepat targets (lspci, ip -d a, INI, YAML) share no parser:

    stream   every line is its own stanza
    header   a stanza starts at an INI [section], a PCI BDF, or a file:line:
             prefix, and runs until the next one
    indent   a stanza starts at a line with N leading spaces and runs until a
             line with N or fewer

  Chunking is lossless: every non-blank input line appears exactly once, in
  order, across the emitted stanzas. Only blank separator lines are dropped.
  --mode auto picks a heuristic from the first --sample lines and reports its
  choice on stderr.

  MATCHING
  Each stanza is scanned for the semantic tokens you name. Validation is
  mathematical where it can be: IP addresses are checked by net/netip, so
  999.888.777.666 is rejected rather than merely unlikely.

  OUTPUT
  Read-only runs stream NDJSON: one complete JSON object per matched stanza,
  written as soon as it is found. An array cannot be emitted until the last match
  is known, and that breaks tail -f, head, and every other streaming consumer.

  stdout carries machine-readable output only. Diffs and prompts go to stderr, so
  nothing can corrupt the JSON stream.

  Next: help grammar | help tokens | help modifiers | help examples`},

	"grammar": {title: "Grammar", body: `GRAMMAR

  [ACTION] [SCOPE] [TARGETS] [MODIFIERS]

  Every component is optional. The action defaults to get, the scope to all, and
  input defaults to stdin.

  EXECUTION ORDER

    1. The positional arguments are split into a grammar and an optional file.
       The trailing argument is the file when it names a readable regular file;
       --file states it explicitly and always wins.
    2. Flags are hoisted out of the argument list, so they may appear before,
       after, or between grammar terms.
    3. The grammar is parsed left to right: action, then scope, then targets,
       then modifiers. An unrecognised token is a parse error naming its
       position, not a silent no-op.
    4. The input is chunked into stanzas (see help overview).
    5. Targets are matched, the scope narrows the result, and the action decides
       what is emitted.
    6. The action runs: a read reports NDJSON to stdout, a replace builds a
       mutation plan and drives the write path (see help modifiers).

  PRECEDENCE AND COMBINING

    - A grammar clause always beats the equivalent flag. "with context 3"
      overrides --context 5.
    - "require all" changes target matching from any-match to all-match. It does
      not affect scopes.
    - Scope is applied after matching and after require all, so "first 3" counts
      matches that already satisfied every target.
    - Modifiers are order-independent: "yolo spot 5" and "spot 5 yolo" parse the
      same way. Where two conflict, the write mode is resolved in the order
      spot, yolo, default (see help modifiers).

  ACTIONS

    Each action below states what it does, whether it can write, and which stream
    carries its result.

    get                 read-only    stdout: NDJSON
      Reports every stanza that matched. This is the default action.

    keep                read-only    stdout: NDJSON
      Identical output to get. Named for the intent of keeping matches while
      discarding the rest, which reads better in a pipeline.

    drop                read-only    stdout: NDJSON
      Reports the complement: the stanzas that did NOT match. Useful for pruning
      noise out of a capture before reading it.

    replace             WRITES       stdout: JSON summary, stderr: diff
      Rewrites matching text. The only action that can touch disk, and the only
      one whose stdout is a single summary object rather than NDJSON. Requires a
      "with ['...']" clause.

    get, keep, and drop never write and never create a backup.

  SCOPES

    Scopes bound how many matches are reported.

    all                 Every match. The default.

    first N             The first N matches.
                        N bounds the number of MATCHES REPORTED, not the number
                        of input lines examined. "get first 3 [ip]" returns three
                        matching stanzas even when it must read past
                        non-matching lines to find them, and it stops reading as
                        soon as it has them. A bare "first" means first 1.

    last N              The last N matches. Only N results are retained in
                        memory, so this stays bounded on a large input.

    today               Matches whose text contains today's date. This is a text
                        heuristic, not a date parser. Recognised forms:
                          2026-02-10    ISO / RFC3339
                          Feb 10        syslog
                          Feb 02        syslog, zero padded
                          02/10/2026    US
                          10/02/2026    EU
                          2026/02/10    slash ISO
                          Mon Feb 2     RFC1123 style

    stanza N            One stanza by 1-based index.

  TARGETS

    A bracketed list of names, a bare quoted literal, or a single name:

      get all [bdf, numa]        two tokens
      get all [ip]               one token
      get keep ['link is down']  an exact literal string
      get all ip                 brackets optional for a single form

    With no target, and scope all, every stanza is reported.

    Full token reference: help tokens

  MODIFIERS

    Full modifier reference: help modifiers`},

	"tokens": {title: "Tokens", body: `SEMANTIC TOKENS

  A token is a named pattern with a validator. Each is looked up
  case-insensitively and matched against a whole stanza, not line by line, so a
  value split across lines is still found.

  With no --tokens flag, every registered token runs and every stanza is
  reported. Naming tokens selects them, and switches to filter mode: only
  stanzas matching at least one named token are emitted.

  ---------------------------------------------------------------------------
  ip
  ---------------------------------------------------------------------------
  IPv4 and IPv6 addresses.

  Validation is delegated to Go's net/netip.ParseAddr rather than a
  hand-written octet count, so octet boundaries are enforced mathematically
  rather than by regex. A pattern only proposes candidates; netip decides.

    accepted    10.0.0.1        0.0.0.0        255.255.255.255
                2001:db8::1     ::1            fe80::1

    rejected    999.888.777.666     octet above 255
                10.0.0.256          octet above 255
                010.1.1.1           leading zeros are ambiguous
                1.2.3               too few octets
                1.2.3.4.5           too many octets
                10.0.0.1/24         a prefix, not an address
                10.0.0.1:8080       host:port is not an address

  Edge cases:
    - A prefix length is not part of the address, so 10.0.0.1/24 yields
      10.0.0.1.
    - A broadcast address is a valid address and is reported.
    - Timestamps such as 12:00:00 are not IPv6: the extractor requires two
      colons and a hex digit start, which a clock time does not satisfy.

  ---------------------------------------------------------------------------
  mac
  ---------------------------------------------------------------------------
  MAC addresses in all three common conventions.

    aa:bb:cc:dd:ee:ff     colon separated
    aa-bb-cc-dd-ee-ff     dash separated
    aabb.ccdd.eeff        Cisco dotted

  The three separators are matched by disjoint patterns, because a single
  expression would have to permit mixed separators within one address, which is
  never valid.

  Timestamp disambiguation (a documented heuristic, with a cost):
  A log timestamp and a MAC address are frequently the same shape. A
  colon-separated candidate whose first three octets read as a valid time of day
  (HH <= 23, MM <= 59, SS <= 59) is treated as a timestamp and dropped:

    Timestamp 12:00:14:ab:12:a5 and MAC 38:00:14:ab:12:a5
      -> reports only 38:00:14:ab:12:a5, because 0x38 exceeds 23 as an hour

  Two exceptions are honoured: the all-zero null address and the all-ff
  broadcast address are always reported, because both are legitimate and appear
  in ip link output.

  THE COST: the small fraction of vendor prefixes whose three leading octets fall
  inside 00-23:00-59:00-59 (00:11:22:... for example) are not reported, because
  they are indistinguishable from a timestamp by shape alone. The trade-off is
  deliberate: in log analysis a false timestamp match is constant noise, whereas
  a missed MAC still appears when reported in dash or dotted form.

  ---------------------------------------------------------------------------
  bdf
  ---------------------------------------------------------------------------
  PCI Bus:Device.Function, as printed by lspci.

    0000:41:00.0     with a four-hex-digit domain
    41:00.0          without a domain
    00:1f.6          short bus form

  Validation:
    - The function nibble must be 0-7. A PCI function has no higher values, and
      this is exactly what stops a clock time such as 12:00:00 from being read as
      a PCI address.
    - The device number must not be 0xff, which lspci never emits.

  ---------------------------------------------------------------------------
  numa
  ---------------------------------------------------------------------------
  The node number from a NUMA label. The first integer after the colon wins.

    NUMA node: 0            -> 0
    NUMA node(s): 2         -> 2
    NUMA nodes: 4           -> 4
    NUMA node0 CPU(s): 0-15 -> 0

  A stanza mentioning several nodes reports each distinct number.

  ---------------------------------------------------------------------------
  error                                          (structured, the default)
  ---------------------------------------------------------------------------
  Errors that announce themselves, and nothing else. Matched forms:

    [ERROR] [error] [ERR] [CRITICAL] [CRIT] [FATAL] [PANIC]   bracketed level
    ERR:  error:  ERROR:                                        prefix form
    failed  failure  critical  fatal  panic                     standalone word

  Deliberately NOT matched: a bare "error" word. On real logs it floods results
  with negations ("Error: none", "no errors found") and identifiers
  ("error_count"). Use error_loose when that recall is worth the noise.

  Matches are reported as the bare keyword, so ERR: yields "ERR" without the
  colon, and the bracketed form yields "ERROR" without the brackets.

  ---------------------------------------------------------------------------
  error_loose                                    (unstructured, opt-in)
  ---------------------------------------------------------------------------
  Everything error matches, plus any word beginning with err, error, or fail.
  This is the override for catching unstructured prose:

    no errors found        matched
    an error occurred      matched
    2 failures detected    matched
    the job errored        matched
    error_count=0          matched (identifier shaped text is caught too)

  Use the strict error token when that last case is unacceptable.

  ---------------------------------------------------------------------------
  link_downgrade
  ---------------------------------------------------------------------------
  A PCIe link that trained below its own capability: the signature of a bad
  riser, a mis-seated card, or a slot wired narrower than the card.

  It compares the advertised maximum on an LnkCap line against the negotiated
  state on an LnkSta line, checking speed and width independently:

    LnkCap: Speed 32GT/s, Width x4
    LnkSta: Speed 2.5GT/s, Width x4
      -> "link downgraded: speed 2.5GT/s of 32GT/s"

  Reading link state requires PCI configuration space, so a non-root lspci -vv
  omits these lines and nothing is reported. That means no false positives on an
  unprivileged run.

  INTERPRETATION: root ports commonly idle at low link speed when nothing is
  downstream, so a flagged bridge is usually benign. A flagged endpoint (an NVMe
  drive or a NIC) is the one worth investigating. Pair it with bdf and require
  all to list only the real cases.

  ---------------------------------------------------------------------------
  Custom tokens
  ---------------------------------------------------------------------------
  Define your own in ~/.vibepat/custom.yaml:

    tokens:
      sitename:
        regex: '\bsite\s+(\S+)'
        description: site code
      serial:
        regex: 'SN[:=]\s*(\w+)'

  Quote regexes with SINGLE quotes. In a YAML double-quoted scalar, \s is an
  invalid escape and the file fails to parse.

  Rules:
    - When a pattern has a capture group, the first group is reported, so
      'node(\d+)' yields 0 rather than node0.
    - Custom tokens are additive. A definition that would shadow a built-in token
      is rejected, as is an invalid regex or a malformed file. A broken custom
      file is a hard error, never a silent skip.
    - A world-writable custom.yaml is refused, since any local user could
      otherwise inject patterns into a root-run tool.
    - --no-custom-tokens ignores the file entirely.`},

	"modifiers": {title: "Modifiers", body: `MODIFIERS AND FLAGS

  Modifiers appear after the targets in the grammar. Flags are the equivalent
  command-line switches and may be written anywhere.

  ===========================================================================
  TARGET SELECTION
  ===========================================================================

  with ['X']                 grammar      --replace OLD=NEW (flag, different form)
    The replacement value. Required by replace. Distinct from an omitted clause,
    which is a syntax error, so an intentional empty replacement ("delete this")
    is expressible as with [''].

    For an ip target the value may use the fourth-octet wildcard or a CIDR
    prefix; both rewrite the network portion and preserve the host portion:

      replace ip ['10.0.1.X']    with ['10.50.1.X']    10.0.1.7 -> 10.50.1.7
      replace ip ['10.0.1.0/24'] with ['10.50.1.X']    10.0.1.7 -> 10.50.1.7
                                                       10.9.9.7 -> untouched

    X is valid only as the fourth octet. Using it elsewhere is a parse error
    rather than a silent non-match.

  require all                grammar
    Switches target matching from "any target matches" to "every target must
    match". The default is any-match, which is right for exploration.

    It matters when one target is present on everything:

      get all [bdf, link_downgrade]              all 36 devices, bdf matches all
      get all [bdf, link_downgrade] require all  only the degraded ones

    Scope is applied after require all, so "first 3" counts matches that already
    satisfied every target.

  ===========================================================================
  CONTEXT
  ===========================================================================

  with context N             grammar
  --context N                flag, default 0
    Embed the N lines PRECEDING each match, oldest first. The matched stanza is
    never included: the buffer is snapshotted before the stanza is pushed.

    The grammar clause overrides the flag, so a script can set a default with
    --context and a single command can raise it.

    Read-only queries only. replace rejects "with context" because a rewrite
    report describes changes rather than surrounding text.

    Context respects chunking: in indent mode the preceding lines are the lines
    that came before the stanza, which may belong to the previous stanza.

  ===========================================================================
  WRITE MODES
  ===========================================================================

  These decide how a replace is committed. Without one, replace is interactive:
  it prints a full diff to stderr and prompts.

  dryrun                     grammar      --dryrun (flag)
    Compute the diff and the JSON summary, then stop. No file change, no backup,
    no temp file left behind. Composes with every other mode, and is the right
    first step for any unfamiliar change.

  yolo  /  force             grammar      --exec yolo (flag)
    Write immediately with no prompt, and emit the JSON summary for CI. force is
    an exact alias.

  spot N                     grammar      --exec spot --spot N (flag)
    Show N randomly chosen diffs, then prompt ONCE for the entire batch.
    Confirming commits every mutation, not only the N displayed: showing a sample
    and writing only that sample would silently discard the rest of the work.
    Requires the changes to be homogeneous enough that a sample is representative.

  COMBINING THE THREE

    Order-independent. The mode is resolved in this order:
      spot    wins if present
      yolo    otherwise, if present
      default interactive, otherwise
    dryrun is orthogonal and overrides all three, because it never writes.

  ===========================================================================
  INPUT AND CHUNKING
  ===========================================================================

  --mode M                   flag, default auto
    stream | header | indent | auto. Auto sniffs the first --sample lines and
    reports its choice on stderr.

  --sample N                 flag, default 100
    Leading lines inspected when --mode is auto.

  --file PATH                flag
    Input file. Overrides the positional path inference, which is the escape
    hatch when a grammar token happens to name an existing file.

  --tokens A,B               flag
    Tokens to match, comma separated. Supplying this enables filter mode.

  --no-custom-tokens         flag
    Do not load ~/.vibepat/custom.yaml.

  ===========================================================================
  OUTPUT
  ===========================================================================

  no-color                   grammar      --no-color (flag)
    Disable ANSI colour in the rendered diff.

    Colour is enabled only when the output stream is a real terminal, detected
    with a termios ioctl rather than a character-device check (which would
    wrongly accept /dev/null). Piped output and CI logs therefore stay clean
    without needing the flag.

  ===========================================================================
  SAFETY
  ===========================================================================

  Every one of these is enforced by a test:

    - stdout is always valid JSON. The diff goes to stderr in every mode.
    - dryrun never writes and never creates a backup.
    - A declined prompt, or a bare Enter, writes nothing.
    - spot N shows N diffs but commits the entire plan.
    - Backups are written once and preserved: <file>.bak is created only if
      absent, so a second run cannot destroy the pristine original.
    - Writes are atomic: temp file in the same directory, fsynced, then renamed
      over the target. A crash or full disk leaves the original intact.
    - Permissions are preserved on both the backup and the rewritten file.
    - A plan is validated before you are asked to approve it: an out-of-range
      line, two changes to one line, or an OriginalText that no longer matches
      the file on disk is refused.
    - A piped input cannot be replaced. That is a hard error, not a silent
      no-op.

  FLAG INDEX

    --mode       --sample     --file       --tokens     --no-custom-tokens
    --context    --exec       --spot       --dryrun     --no-color
    --help       --version    -i

    Every flag has a longer description above or in "vibepat help grammar".`},

	"examples": {title: "Examples", body: `EXAMPLES

  Copyable recipes for the jobs vibepat exists for. Replace file paths with your
  own.

  ===========================================================================
  HARDWARE
  ===========================================================================

  Find degraded PCIe links on a compute node
    lspci -vvv | vibepat get all [bdf, link_downgrade] require all

    Without require all this reports every device, because every device has a
    bdf. With it, only links that trained below their capability survive.
    Remember that a flagged bridge is usually an idle root port; a flagged
    endpoint is the real finding.

  Inventory a machine's PCI topology
    vibepat --mode header get all [bdf] lspci.txt

  Correlate devices with their NUMA placement
    lspci -vvv | vibepat get all [bdf, numa] require all

  Capture GPU and NIC placement before a firmware change
    lspci -vv > before.txt
    vibepat --mode header get all [bdf] before.txt > before.json

  ===========================================================================
  LIVE LOGS
  ===========================================================================

  Watch errors with surrounding context, live
    journalctl -fu kubelet | vibepat keep all [error] with context 5

    Given this stream:

      nvme0n1: pci function 0000:04:00.0
      nvme0n1: queue depth 1024
      nvme0n1: starting reset
      nvme0n1: nvme_reset_ctrl started
      nvme0n1: resetting controller
      nvme0n1: I/O error, dev nvme0n1, sector 12345

    the last line matches, and the object carries the five before it:

      {"matched_tokens":{"error":["ERROR"]},"context_lines":[
        "nvme0n1: pci function 0000:04:00.0",
        "nvme0n1: queue depth 1024",
        "nvme0n1: starting reset",
        "nvme0n1: nvme_reset_ctrl started",
        "nvme0n1: resetting controller"],
        "stanza":{"lines":["nvme0n1: I/O error, dev nvme0n1, sector 12345"],...

    Output appears per match, not at exit, so this works on an endless stream.
    The five preceding lines arrive in context_lines, oldest first, and never
    include the matching line itself. Lower the number when five is more context
    than you want.

  Stop at the first error of a boot
    journalctl -b | vibepat keep first 1 [error] with context 10

    $ printf 'addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\n' > /tmp/w.txt
    $ vibepat keep first 1 [ip] with context 1 /tmp/w.txt
    {"matched_tokens":{"ip":["10.0.0.1"]},"context_lines":[],"stanza_index":1,...

    Only the first match is reported, and the run stops there. context_lines is
    empty because nothing precedes the first line; on a later match it would
    carry the preceding line.

  Catch unstructured errors the strict token ignores
    journalctl -b | vibepat keep all [error_loose]

  Extract only today's errors
    vibepat get today [error] /var/log/syslog

  Prune non-error noise out of an incident capture
    vibepat drop all [error] incident.log > errors-only.log

    $ printf 'addr 10.0.0.1\nplain-b\nplain-c\n' | vibepat drop all [ip]
    {"matched_tokens":{},"stanza":{"lines":["plain-b"],...
    {"matched_tokens":{},"stanza":{"lines":["plain-c"],...

    drop reports the complement, so this writes the lines that did NOT match.
    Their matched_tokens is an empty object rather than absent, so the record
    shape never changes. To keep the matches instead, use keep.

  ===========================================================================
  NETWORK
  ===========================================================================

  Pull addresses and MACs out of an interface dump
    ip -d a | vibepat get all [ip, mac]

  List every address on a host, one per line
    vibepat get all [ip] links.txt | jq -r '.matched_tokens.ip[]'

  Audit a config for addresses outside the expected subnet
    vibepat get all [ip] ./interfaces

  ===========================================================================
  CONFIG REWRITES
  ===========================================================================

  Scoped subnet migration, with a safety spot-check
    vibepat replace ip ['10.0.1.0/24'] with ['10.50.1.X'] spot 5 /etc/network/interfaces

    Five random diffs are shown, then one prompt commits all of them. Addresses
    outside 10.0.1.0/24, such as a gateway on 10.9.9.7, are untouched.

  Always preview before an unfamiliar change
    vibepat replace ip ['10.0.1.0/24'] with ['10.50.1.X'] dryrun /etc/network/interfaces

  CI/CD non-interactive bulk update
    vibepat replace driver ['vfio-pci'] with ['amdgpu'] yolo config.ini

    No prompt, and a JSON summary on stdout for the pipeline to inspect:

    {
      "status": "applied",
      "source_path": "/etc/vfio.conf",
      "backup_path": "/etc/vfio.conf.bak",
      "applied": 3,
      "total": 3,
      "sampled": 3,
      "mutations": [ { "line_number": 12, "original_text": "...", ... } ],
      "error": ""
    }

    status is one of applied, no_changes, aborted, dryrun, or error, so a
    pipeline can branch on it. The diff still goes to stderr, so the summary on
    stdout stays parseable. Pair yolo with dryrun in a pull-request check and
    yolo in the merge job.

  Redact MAC addresses before sharing a capture
    vibepat replace mac with ['REDACTED'] yolo capture.txt

    A bare token name with no pattern replaces every value of that token. The
    original is preserved at capture.txt.bak.

  ===========================================================================
  COMPOSING WITH OTHER TOOLS
  ===========================================================================

  vibepat emits NDJSON, one complete JSON object per line, so any JSON processor
  composes directly, and the streaming behaviour survives the pipe.

    vibepat get all [ip] links.txt | jq -r '.matched_tokens.ip[]'
    vibepat get all [bdf, link_downgrade] require all lspci.txt \
      | jq -c 'select(.matched_tokens.link_downgrade)'
    vibepat get all [ip] links.txt | jq -c '{host: .stanza.lines[0], addrs: .matched_tokens.ip}'

  Because each line is self-contained, head, grep, and sort -u work as expected:

    vibepat get all [ip] links.txt | head -1
    vibepat get all [ip] links.txt | grep -c 10.0

  ===========================================================================
  GETTING HELP
  ===========================================================================

    vibepat help              the complete manual
    vibepat help grammar      syntax, order, precedence, actions, scopes
    vibepat help tokens       every token, validation, custom tokens
    vibepat help modifiers    flags, write modes, safety
    vibepat help examples     this page
    vibepat -i                interactive mode; "help" works there too`},
}

// helpIndex is the table of contents, assembled from helpTopics so it cannot
// list a topic that does not exist or omit one that does.
const helpIndex = `TOPICS

  Run "vibepat help <topic>" for a single topic, or "vibepat help" for all.

`

// helpPageFor returns the rendered page for a topic name, resolving aliases.
//
// An empty topic resolves to the walkthrough, so asking for help with no topic
// teaches rather than buries.
//
// An unknown topic is an error rather than a silent fallback to the full manual,
// because a user who mistyped a topic should be told, and a full manual dumped
// in response to a typo buries the correction.
func helpPageFor(topic string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(topic))

	if canonical, ok := helpAliases[key]; ok {
		key = canonical
	}
	if key == "" {
		key = "all"
	}

	switch key {
	case "index":
		return helpPreamble + "\n" + renderHelpIndex(), nil
	case "all":
		return helpPreamble + "\n" + renderHelpIndex() + "\n" + renderAllTopics(), nil
	}

	page, ok := helpBodies[key]
	if !ok {
		return "", fmt.Errorf("unknown help topic %q\n\navailable topics: %s",
			topic, strings.Join(helpTopics, ", "))
	}
	return helpPreamble + "\n" + page.body, nil
}

// renderHelpIndex lists every topic with its description.
func renderHelpIndex() string {
	var b strings.Builder
	b.WriteString(helpIndex)

	width := 0
	for _, t := range helpTopics {
		if len(t) > width {
			width = len(t)
		}
	}
	for _, t := range helpTopics {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, t, helpTopicDescriptions[t])
	}
	fmt.Fprintf(&b, "  %-*s  %s\n", width, "all", "every topic in sequence")
	fmt.Fprintln(&b)
	return b.String()
}

// renderAllTopics concatenates the canonical pages in order, with separators.
func renderAllTopics() string {
	var b strings.Builder
	for i, t := range helpTopics {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Repeat("=", 75))
			b.WriteString("\n\n")
		}
		b.WriteString(helpBodies[t].body)
		b.WriteString("\n")
	}
	return b.String()
}

// HelpTopics returns the canonical topic names, for completion and tests.
func HelpTopics() []string {
	out := make([]string, len(helpTopics))
	copy(out, helpTopics)
	return out
}

// HelpCompletions returns every accepted topic name, including aliases, sorted
// for stable display.
func HelpCompletions() []string {
	seen := map[string]bool{"all": true}
	for _, t := range helpTopics {
		seen[t] = true
	}
	for alias := range helpAliases {
		if alias != "" {
			seen[alias] = true
		}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// helpEnv bundles the streams and terminal facts the help writer needs.
type helpEnv struct {
	// Out receives the help text.
	Out io.Writer
	// IsTerminal reports whether Out is an interactive terminal, which is what
	// decides whether a pager is used.
	IsTerminal bool
}

// WriteHelp renders a topic to env.Out, paging when the destination is a
// terminal.
//
// An empty topic prints the complete manual, which is what both --help and a
// bare "help" mean.
func WriteHelp(topic string, env helpEnv) error {
	text, err := helpPageFor(topic)
	if err != nil {
		return err
	}
	return writePaged(env.Out, text, env.IsTerminal)
}

// WriteHelpIndex writes the lightweight table of contents, used by --help so
// that the flag stays a quick orientation rather than a wall of text.
func WriteHelpIndex(env helpEnv) error {
	text, err := helpPageFor("index")
	if err != nil {
		return err
	}
	return writePaged(env.Out, text, env.IsTerminal)
}

// pagerCandidates returns the pagers to try, in order.
//
// $PAGER is honoured first, including any arguments it carries ("less -R").
// The fallbacks matter because a minimal host may have neither less nor more,
// and in that case the text is simply printed.
func pagerCandidates() [][]string {
	var out [][]string
	if p := strings.TrimSpace(os.Getenv("PAGER")); p != "" {
		out = append(out, strings.Fields(p))
	}
	out = append(out, []string{"less", "-R"}, []string{"more"})
	return out
}

// writePaged writes text to w, through a pager when interactive is true.
//
// A missing pager, or one that exits early because the reader quit, is not an
// error: the fallback is to print the text directly. Help output must never fail
// because of how it is displayed.
func writePaged(w io.Writer, text string, interactive bool) error {
	if !interactive {
		_, err := io.WriteString(w, text)
		return err
	}

	var lastErr error
	for _, candidate := range pagerCandidates() {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			lastErr = err
			continue
		}

		cmd := exec.Command(path, candidate[1:]...)
		cmd.Stdin = strings.NewReader(text)
		cmd.Stdout = w
		cmd.Stderr = os.Stderr

		err = cmd.Run()
		if err == nil {
			return nil
		}
		// The reader quitting a pager closes the pipe, which surfaces as a
		// write error or a signal. That is a normal way to stop reading.
		if isPagerQuit(err) {
			return nil
		}
		lastErr = err
	}

	// No usable pager: print the text rather than losing it.
	if _, err := io.WriteString(w, text); err != nil {
		return err
	}
	_ = lastErr
	return nil
}

// isPagerQuit reports whether err is the pager exiting early, which is what
// happens when the reader presses q. It is not a failure worth reporting.
func isPagerQuit(err error) bool {
	if err == nil {
		return false
	}
	// A broken pipe while the pager is being written to.
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Any non-zero pager exit is the pager's own decision to stop.
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "signal")
}

// runHelp implements the "help [topic]" CLI form.
//
// It returns handled=false when the arguments are not a help request, so the
// caller can fall through to normal grammar parsing.
func runHelp(args []string, env helpEnv) (handled bool, err error) {
	if len(args) == 0 || !strings.EqualFold(args[0], "help") {
		return false, nil
	}

	// "vibepat help" lands on the walkthrough, so a newcomer is taught rather than
	// buried. "vibepat help all" is the complete manual, and any other argument
	// is a topic.
	switch len(args) {
	case 1:
		return true, WriteHelp("start", env)
	case 2:
		return true, WriteHelp(args[1], env)
	default:
		return true, fmt.Errorf("help takes at most one topic, got %d arguments", len(args)-1)
	}
}
