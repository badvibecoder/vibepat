# Development

## Layout

| Path | Contents |
| :--- | :--- |
| `src/` | The entire Go package. All source and tests live here. |
| `src/registry/` | The semantic token registry, a separate package. |
| `src/testdata/` | Test fixtures, including the real `lspci` capture. |
| `scripts/` | Build tooling. |
| `docs/` | This documentation. |
| `go.mod`, `go.sum` | Module definition. Must stay at the repository root; the Go toolchain requires it. |
| `Makefile` | Entry points for build, test, and release. |

The repository root holds only the module file, the Makefile, the top-level
README, and directories — no Go source. That keeps the tree easy to read and
makes the paste into a larger repository a single drop-in.

## Build

All toolchain state is kept **inside this repository**. The default Go paths
(`~/.cache/go-build`, `~/go/pkg/mod`) are not writable in the build sandbox, and
pinning them also keeps a release reproducible rather than dependent on whatever
is in a developer's global cache.

```sh
make build          # produces ./vibepat for the host platform
make test           # go test ./... -count=1
make vet            # go vet ./...
make fmt            # gofmt -l -w .
make clean          # remove the binary and the local Go caches

make cache-clean     # reclaim the build cache, keep downloaded modules
make cache-clean-all # reclaim both

make release        # linux/amd64 + windows/amd64 into dist/, with checksums
make release-clean  # dist-clean, then release
make release-verify # assert the artifacts are static, runnable, checksummed
make help           # list every target
```

To invoke `go` directly rather than through `make`, export the same variables:

```sh
export GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache \
       GOPATH=$PWD/.gopath GOSUMDB=off
```

## Release builds

`make release` produces two portable, statically linked binaries in `dist/`:

| Target | Artifact |
| :--- | :--- |
| `linux/amd64` | `dist/vibepat-linux-amd64` |
| `windows/amd64` | `dist/vibepat-windows-amd64.exe` |

The same build runs standalone, which is what CI should call:

```sh
scripts/build_release.sh
DIST_DIR=/tmp/out scripts/build_release.sh   # override the output directory
```

### What the release guarantees

- **`CGO_ENABLED=0`.** The Linux binary has no shared library dependencies, so it
  runs on any distribution or minimal container regardless of the host's glibc:

  ```sh
  $ file dist/vibepat-linux-amd64
  ELF 64-bit LSB executable, x86-64, statically linked, stripped
  $ ldd dist/vibepat-linux-amd64
          not a dynamic executable
  ```

- **Stripped.** `-ldflags="-s -w"` removes the symbol table and DWARF debug info.
  The version survives because it is stamped in with `-X main.version=...`, which
  is why `version` is a `var` and not a `const` — the linker cannot override a
  constant.
- **`-trimpath`**, so no build-host paths are embedded and the output is
  reproducible.
- **Reproducible bytes.** Two builds from a wiped cache produce identical
  checksums.
- **Isolated caches.** Both the Makefile and the script pin `GOCACHE`,
  `GOMODCACHE`, and `GOPATH` inside the repository. Nothing is written to
  `~/.cache` or `~/go`.
- **Per-platform terminal detection.** `tty_unix.go` and `tty_windows.go` provide
  `isTerminal` via a termios ioctl and `GetConsoleMode` respectively; the Unix
  implementation does not compile on Windows, which is why they are separate
  files.

Checksums are generated from inside `dist/`, so `sha256sum -c checksums.txt`
works when run from that directory:

```sh
cd dist && sha256sum -c checksums.txt
```

## Testing

```sh
make test                          # everything
go test -run TestIntegration ./... # the end-to-end suite
go test -short ./...               # skip the cross-compile tests
```

The suite is layered deliberately:

| Layer | Files | Purpose |
| :--- | :--- | :--- |
| Unit | `*_test.go` beside each source file | One behaviour per test, fast. |
| Integration | `integration_test.go` | Real strings through the whole pipeline, asserting on emitted JSON or bytes on disk. Nothing is mocked. |
| Golden | `golden_test.go` | The real `lspci` capture, asserting chunking is lossless and results match an independent source. |
| Streaming | `stream_test.go` | That output is genuinely incremental, using a still-open pipe. |
| Build | `build_test.go` | That both targets cross-compile, the Linux binary is static, and the release script is intact. |

Two conventions worth keeping:

- **Verify against real data, not invented data.** Hand-written fixtures
  repeatedly hid bugs that `src/testdata/lspci.txt` exposed — a mis-calibrated
  detection threshold, tab-versus-space indentation, and the exact position of
  lspci's `(downgraded)` parenthetical. When adding a heuristic, test it against
  real output from a real machine.
- **Prove a test can fail.** After fixing a bug, revert the fix and confirm the
  new test actually fails. A test that passes for the wrong reason is worse than
  no test, because it buys false confidence.
