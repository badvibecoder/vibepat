# vibepat build system.
#
# All toolchain state lives inside the repository. The default Go locations
# (~/.cache/go-build, ~/go/pkg/mod) are not writable in the build sandbox, and
# pinning them here also keeps a release build reproducible rather than dependent
# on whatever happens to be in a developer's global cache.
export GOCACHE     := $(CURDIR)/.gocache
export GOMODCACHE  := $(CURDIR)/.gomodcache
export GOPATH      := $(CURDIR)/.gopath
export GOSUMDB     := off
export GOFLAGS     := -mod=mod

# Static builds everywhere. Pinning it here rather than only in the release
# script keeps `make test` and `make release` compiling with identical settings,
# so they share a build cache instead of invalidating each other, and the Linux
# binary never gains a glibc dependency by accident.
export CGO_ENABLED := 0

# Release artifacts land here: binaries plus checksums.txt.
DIST_DIR := $(CURDIR)/dist

# -s strips the symbol table and -w strips DWARF debug info, which is most of an
# unstripped Go binary. Release builds stamp the version so a stripped binary can
# still report what it is.
VERSION  := $(shell sed -n 's/^var version = "\(.*\)"$$/\1/p' src/main.go | head -1)
LDFLAGS  := -s -w
ifneq ($(VERSION),)
LDFLAGS  += -X main.version=$(VERSION)
endif

.PHONY: all build test fmt vet lint clean \
        release release-clean release-verify dist checksums \
        dist-clean cache-clean cache-clean-all help

all: build

# --- development -----------------------------------------------------------

# The Go package lives in src/, so the repository root stays free of source and
# the whole buildable tree is one directory.
build:
	go build -o vibepat ./src

test:
	go test ./... -count=1

vet:
	go vet ./...

# Enumerate source files explicitly: `gofmt -w .` would descend into the local
# module cache and report unformatted third-party code.
fmt:
	gofmt -l -w src/*.go src/registry/*.go

# The local caches hold downloaded modules whose files are read-only, so the
# permission bits are relaxed before removal. A leading `-` keeps clean
# best-effort, since a partially removed cache is still a usable cache.
clean:
	-rm -f vibepat
	-chmod -R u+w .gocache .gomodcache .gopath 2>/dev/null
	-rm -rf .gocache .gomodcache .gopath

# Remove only the build output, leaving the caches so the next build stays fast.
dist-clean:
	-rm -rf "$(DIST_DIR)"

# The build cache is by far the largest thing in the workspace (hundreds of MB)
# and is regenerated on demand, so it is worth reclaiming on its own. The module
# cache is kept unless you ask for -mod, because re-downloading is slower than
# re-compiling.
cache-clean:
	@echo "==> before"
	@du -sh .gocache .gomodcache .gopath 2>/dev/null || true
	-chmod -R u+w .gocache .gopath 2>/dev/null
	-rm -rf .gocache .gopath
	@echo "==> after"
	@du -sh .gocache .gomodcache .gopath 2>/dev/null || true

# Also reclaims the module cache. The next build re-downloads every dependency.
cache-clean-all:
	-@$(MAKE) --no-print-directory cache-clean
	-chmod -R u+w .gomodcache 2>/dev/null
	-rm -rf .gomodcache
	@echo "==> module cache removed; the next build will re-download dependencies"

# --- release ---------------------------------------------------------------

# Build portable static binaries for linux/amd64 and windows/amd64, then write
# dist/checksums.txt. Delegates to the script so the same build can be run in CI
# without make.
release:
	@DIST_DIR="$(DIST_DIR)" "$(CURDIR)/scripts/build_release.sh"

# A release from a dirty output directory can silently mix artifacts from two
# revisions and produce checksums that describe neither. This target exists so
# that mistake is a single deliberate command.
release-clean:
	@$(MAKE) dist-clean
	@$(MAKE) release

# Verify the artifacts rather than trusting the build log: the Linux binary must
# have no dynamic dependencies and must actually run.
release-verify:
	@echo "==> artifact statically linked?"
	@file "$(DIST_DIR)/vibepat-linux-amd64"
	@if command -v ldd >/dev/null 2>&1; then \
		if ldd "$(DIST_DIR)/vibepat-linux-amd64" 2>&1 | grep -qi 'not a dynamic executable\|statically linked'; then \
			echo "    OK: no dynamic dependencies"; \
		else \
			echo "    FAIL: the Linux binary has dynamic dependencies" >&2; \
			ldd "$(DIST_DIR)/vibepat-linux-amd64" >&2; \
			exit 1; \
		fi; \
	fi
	@echo "==> linux binary runs"
	@$(DIST_DIR)/vibepat-linux-amd64 --version
	@echo "==> windows binary is a PE executable"
	@file "$(DIST_DIR)/vibepat-windows-amd64.exe"
	@echo "==> checksums verify"
	@cd "$(DIST_DIR)" && sha256sum -c checksums.txt

# --- convenience -----------------------------------------------------------

checksums:
	@cd "$(DIST_DIR)" && sha256sum vibepat-* >checksums.txt && cat checksums.txt

help:
	@echo "vibepat build targets"
	@echo "  build           build for the host platform into ./vibepat"
	@echo "  test            run the full test suite"
	@echo "  vet, fmt        go vet / gofmt the project sources"
	@echo "  clean           remove the binary and the local Go caches"
	@echo "  cache-clean     reclaim the build cache (hundreds of MB), keep modules"
	@echo "  cache-clean-all reclaim both caches"
	@echo "  dist-clean      remove dist/ only"
	@echo "  release         build linux/amd64 and windows/amd64 into dist/"
	@echo "  release-clean   dist-clean followed by release"
	@echo "  release-verify  assert the artifacts are static, runnable, checksummed"
