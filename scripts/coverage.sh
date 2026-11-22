#!/bin/sh
# Runs all tests and fails if total statement coverage is below the
# threshold (default 70%, override with COVERAGE_MIN).
set -eu

min="${COVERAGE_MIN:-70}"
profile="$(mktemp "${TMPDIR:-/tmp}/bitwave-cover.XXXXXX")"
trap 'rm -f "$profile"' EXIT

cd "$(dirname "$0")/.."
go test -count=1 -coverprofile="$profile" ./...

total="$(go tool cover -func="$profile" | awk '/^total:/ { sub("%", "", $3); print $3 }')"
if awk -v t="$total" -v m="$min" 'BEGIN { exit !(t + 0 < m + 0) }'; then
	echo "coverage ${total}% is below the required ${min}%" >&2
	exit 1
fi
echo "coverage ${total}% (required ${min}%)"
