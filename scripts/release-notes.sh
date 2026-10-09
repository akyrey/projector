#!/bin/sh
# Prints the CHANGELOG.md section for a version (e.g. v0.1.0), without its
# heading. Exits 1 when the section is missing or empty.
set -eu

version="${1:?usage: release-notes.sh <version> [changelog]}"
changelog="${2:-CHANGELOG.md}"

notes="$(awk -v heading="## [$version]" '
	index($0, heading) == 1 { found = 1; next }
	found && /^## \[/ { exit }
	found && /^\[[^]]+\]: / { exit }
	found { print }
' "$changelog" | sed -e '/./,$!d')"

if [ -z "$notes" ]; then
	echo "no changelog entry for $version in $changelog" >&2
	exit 1
fi
printf '%s\n' "$notes"
