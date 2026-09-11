# Third-party notices

`vibepat` is distributed with the following third-party Go modules compiled in.
Each is listed with its license and copyright holder. All are permissive
licenses compatible with redistribution.

## Direct dependencies

### github.com/elk-language/go-prompt

MIT License. Copyright (c) 2023 Mateusz Drewniak; Copyright (c) 2017 Masashi
SHIBATA. A maintained fork of `github.com/c-bata/go-prompt`, used for the
interactive REPL and its TAB completion.

### gopkg.in/yaml.v3

MIT License. Copyright (c) 2011-2019 Canonical Systems, Inc. and contributors.
Used to parse `~/.vibepat/custom.yaml`.

### golang.org/x/sys

BSD 3-Clause License. Copyright 2009 The Go Authors. Used for the termios ioctl
that detects a real terminal.

## Indirect dependencies

| Module | License | Copyright |
| :--- | :--- | :--- |
| `github.com/mattn/go-colorable` | MIT | Yasuhiro Matsumoto |
| `github.com/mattn/go-isatty` | MIT | Yasuhiro Matsumoto |
| `github.com/mattn/go-runewidth` | MIT | Yasuhiro Matsumoto |
| `github.com/mattn/go-tty` | MIT | Yasuhiro Matsumoto |
| `github.com/pkg/term` | BSD 2-Clause | 2014 David Cheney |
| `github.com/rivo/uniseg` | MIT | 2019 Oliver Kuederle |
| `golang.org/x/exp` | BSD 3-Clause | 2009 The Go Authors |

## Full license texts

The complete license text for each module is distributed with the module itself
in the Go module cache, and is reproducible with:

```sh
go mod download -x
go list -m -f '{{.Path}} {{.Version}}' all
```

## Test fixtures

`testdata/lspci.txt` is `lspci -vv` output captured from a machine owned by the
project author and is covered by this project's license. It contains no
personally identifying information; it is PCI device topology and link state.
