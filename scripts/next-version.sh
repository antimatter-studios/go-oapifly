#!/usr/bin/env bash
# Prints the version a commit gets when it lands on main: the newest v* tag, bumped by the
# commit's conventional-commit type (#21).
#
#   feat:                      minor
#   a breaking change          minor while on 0.x, major from 1.0 (type!: or a
#                              "BREAKING CHANGE:" footer)
#   anything else (fix, docs)  patch
#
# Usage: scripts/next-version.sh [commit]   (default HEAD)
set -euo pipefail

commit="${1:-HEAD}"
latest=$(git tag --list 'v[0-9]*' --sort=-v:refname | head -n 1)
latest=${latest:-v0.0.0}
IFS=. read -r major minor patch <<<"${latest#v}"

subject=$(git log -1 --format=%s "$commit")
body=$(git log -1 --format=%b "$commit")

if [[ "$subject" =~ ^[a-z]+(\([^\)]*\))?!: ]] || grep -qE '^BREAKING[ -]CHANGE:' <<<"$body"; then
  if [ "$major" = 0 ]; then
    minor=$((minor + 1)); patch=0
  else
    major=$((major + 1)); minor=0; patch=0
  fi
elif [[ "$subject" =~ ^feat(\([^\)]*\))?: ]]; then
  minor=$((minor + 1)); patch=0
else
  patch=$((patch + 1))
fi

echo "v${major}.${minor}.${patch}"
