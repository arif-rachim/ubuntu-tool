#!/usr/bin/env bash
# Membangun binary rilis linux/amd64 + linux/arm64 beserta SHA256SUMS di ./dist.
# Dengan --publish: membuat tag git, mendorongnya, dan membuat GitHub release (butuh gh yang sudah login).
#
#   scripts/release.sh v0.1.0             # build + checksum saja
#   scripts/release.sh v0.1.0 --publish   # build + tag + GitHub release
set -euo pipefail

tag="${1:-}"
publish="${2:-}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
	echo "pemakaian: scripts/release.sh vX.Y.Z [--publish]" >&2
	exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
	echo "working tree belum bersih; commit atau stash dulu" >&2
	exit 1
fi
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	echo "tag $tag sudah ada" >&2
	exit 1
fi

make all
rm -rf dist
make build-all VERSION="$tag"
(cd dist && sha256sum ubt-* > SHA256SUMS)
echo
cat dist/SHA256SUMS

if [[ "$publish" == "--publish" ]]; then
	command -v gh >/dev/null || { echo "gh tidak ditemukan; pasang GitHub CLI atau unggah ./dist secara manual" >&2; exit 1; }
	git tag -a "$tag" -m "ubt $tag"
	git push origin "$tag"
	gh release create "$tag" dist/* --title "ubt $tag" --generate-notes
fi
