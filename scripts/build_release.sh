#!/usr/bin/env bash
#
# Build portable, statically linked vibepat release binaries.
#
# Targets:
#   linux/amd64   -> dist/vibepat-linux-amd64
#   windows/amd64 -> dist/vibepat-windows-amd64.exe
#
# Two properties matter here and are both enforced rather than assumed:
#
#   1. CGO_ENABLED=0. Without it the Linux binary links against the host glibc
#      and fails on any distribution with an older libc -- the classic
#      "GLIBC_2.34 not found" failure in a minimal container. With it the binary
#      has no shared library dependencies at all.
#
#   2. Local toolchain state. GOCACHE, GOMODCACHE, and GOPATH are pinned inside
#      the repository, because ~/.cache and ~/go are not writable in the build
#      sandbox and a build that silently falls back to them would not be
#      reproducible.
#
# Usage:
#   scripts/build_release.sh              # build and checksum every target
#   DIST_DIR=/tmp/out scripts/build_release.sh
#
set -euo pipefail

# Resolve the repository root from this script's location, so the script works
# regardless of the caller's working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# --- local toolchain state -------------------------------------------------
export GOCACHE="${REPO_ROOT}/.gocache"
export GOMODCACHE="${REPO_ROOT}/.gomodcache"
export GOPATH="${REPO_ROOT}/.gopath"
export GOSUMDB="${GOSUMDB:-off}"
export GOFLAGS="${GOFLAGS:--mod=mod}"

# --- output ----------------------------------------------------------------
DIST_DIR="${DIST_DIR:-${REPO_ROOT}/dist}"
CHECKSUMS="${DIST_DIR}/checksums.txt"

# -s strips the symbol table, -w strips DWARF debug info. Together they remove
# the Go build id and debug sections that dominate an unstripped binary.
LDFLAGS="-s -w"

# Version metadata is injected when the output can be found, so a stripped
# binary can still report what it is.
VERSION="$(sed -n 's/^var version = "\(.*\)"$/\1/p' src/main.go | head -1 || true)"
if [ -z "${VERSION}" ]; then
	echo "warning: could not read version from main.go; building without it" >&2
fi
if [ -n "${VERSION}" ]; then
	LDFLAGS="${LDFLAGS} -X main.version=${VERSION}"
fi

# target_os:target_arch:output-name
TARGETS=(
	"linux:amd64:vibepat-linux-amd64"
	"windows:amd64:vibepat-windows-amd64.exe"
)

mkdir -p "${DIST_DIR}"

echo "vibepat release build"
echo "  repo        : ${REPO_ROOT}"
echo "  version     : ${VERSION:-<unknown>}"
echo "  output      : ${DIST_DIR}"
echo "  GOCACHE     : ${GOCACHE}"
echo "  GOMODCACHE  : ${GOMODCACHE}"
echo "  GOPATH      : ${GOPATH}"
echo "  CGO_ENABLED : 0"
echo

for target in "${TARGETS[@]}"; do
	IFS=':' read -r target_os target_arch output_name <<<"${target}"
	output_path="${DIST_DIR}/${output_name}"

	printf '  building %-8s %-6s -> %s\n' "${target_os}" "${target_arch}" "${output_name}"

	CGO_ENABLED=0 \
		GOOS="${target_os}" \
		GOARCH="${target_arch}" \
		go build -trimpath -ldflags="${LDFLAGS}" -o "${output_path}" ./src

	if [ ! -f "${output_path}" ]; then
		echo "error: ${output_path} was not produced" >&2
		exit 1
	fi
done

echo
echo "  writing checksums -> $(basename "${CHECKSUMS}")"

# Checksums are generated from inside dist/ so the file lists bare filenames,
# which is what `sha256sum -c` expects when run from that directory.
(
	cd "${DIST_DIR}"
	# Only the release artifacts, so a stale checksums.txt never hashes itself.
	sha256sum vibepat-* >"$(basename "${CHECKSUMS}")"
)

echo
echo "release artifacts:"
for target in "${TARGETS[@]}"; do
	IFS=':' read -r _ _ output_name <<<"${target}"
	printf '  %-32s %10s bytes\n' "${output_name}" "$(stat -c %s "${DIST_DIR}/${output_name}")"
done
echo
cat "${CHECKSUMS}"
