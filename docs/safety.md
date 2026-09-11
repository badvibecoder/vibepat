# Safety model

`replace` is the only action that writes. Everything below is enforced by a test,
not merely intended.

## The default flow

```sh
vibepat replace ip ['10.0.1.X'] with ['10.50.1.X'] ./config
```

1. The input is read and chunked.
2. A mutation plan is built in memory.
3. The plan is **validated** — see below.
4. The full diff is printed to stderr, with colour when stderr is a terminal.
5. `Commit N changes? [y/N]` is prompted on the terminal.
6. Only on `y` is a backup made and the file rewritten.

## Enforced properties

| Property | Detail |
| :--- | :--- |
| **stdout is always valid JSON** | The diff goes to stderr in every mode, including `--dryrun`. |
| **`--dryrun` never writes** | No file change, no backup, no temp file left behind. |
| **A declined prompt writes nothing** | No change, no backup. |
| **Bare Enter means no** | The prompt is fail-safe. |
| **`spot N` shows N, commits all** | Showing a sample and writing only that sample would silently discard the rest of the work. |
| **Backups are written once** | `<file>.bak` is created only if absent, so a second run cannot destroy the pristine original. |
| **Writes are atomic** | Content goes to a temp file in the same directory, is fsynced, then renamed over the target. A crash or full disk leaves the original intact. |
| **Permissions are preserved** | On both the backup and the rewritten file, so a `0600` config does not become world-readable. |
| **Plans are validated before approval** | A plan with an out-of-range line, two changes to one line, or an `OriginalText` that no longer matches the file on disk is refused. |
| **A piped input cannot be replaced** | A hard error rather than a silent no-op. |

## Plan validation

A `ChangePlan` is rejected before the user is asked to approve it if any mutation:

- targets a line outside the input,
- targets a line another mutation already changes,
- expects `OriginalText` that does not match what is on disk.

The third check is what makes a plan safe against a file that changed between
being read and being written.

## Execution modes

| Mode | Behaviour |
| :--- | :--- |
| *default* | Full diff, then one prompt. |
| `yolo` / `force` | No prompt. Writes immediately and emits the JSON summary. For CI. |
| `spot N` | N randomly sampled diffs, then one prompt for the entire batch. A spot-check, not a partial write. |
| `--dryrun` | Diff and summary, never writes. Composes with any mode. |

## Colour

Colour is enabled only when the output stream is a **real terminal**, detected
with a termios ioctl rather than a character-device check (which would wrongly
accept `/dev/null`). Piped output and CI logs therefore stay free of escape
codes. `--no-color` forces it off.
