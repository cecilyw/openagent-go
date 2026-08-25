#!/usr/bin/env bash
# build.sh — convenience build for the openagent-go binary.
#
# Produces <name> (Go CLI/server with the interactive TUI built in as the
# `tui` subcommand). Built with -tags embed (built-in skills bundled) and
# -s -w (stripped, smaller binary). The binary's identity is set via
# ldflags (version.Name / version.Version).
#
# The TUI is pure Go (bubbletea), no longer a separate Rust binary — run
# `./<name> tui` to launch it.
#
# Usage:
#   ./build.sh                         # name=openagent (default)
#   OPENAGENT_BINARY_NAME=myagent ./build.sh
#
# Requires: Go (any recent). No Rust toolchain needed for the TUI; Rust is
# only used for the optional WASM plugin PDK (see examples/plugin/).
set -euo pipefail

NAME="${OPENAGENT_BINARY_NAME:-openagent}"
VERSION="${OPENAGENT_VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

echo "==> Building Go binary: $NAME (version $VERSION, embedded skills)"
# -tags embed: bundle built-in skills (skills/builtin/*) into the binary.
# -s -w: strip symbol table and DWARF debug info for a smaller binary.
go build -tags embed \
         -ldflags "-s -w \
                   -X github.com/yusheng-g/openagent-go/version.Name=$NAME \
                   -X github.com/yusheng-g/openagent-go/version.Version=$VERSION" \
         -o "$NAME" ./cmd/cli/

echo ""
echo "Built:"
echo "  $NAME    (serve/run/keyring + tui subcommand)"
echo ""
echo "Run:  $NAME tui"
