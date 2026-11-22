#!/bin/sh
# Points git at the versioned hooks in .githooks.
set -eu
cd "$(dirname "$0")/.."
chmod +x .githooks/* scripts/*.sh
git config core.hooksPath .githooks
echo "hooks installed: $(git config core.hooksPath)"
