#!/usr/bin/env bash
# Membangun binary rilis linux/amd64 + linux/arm64 beserta SHA256SUMS di ./dist.
# Dengan --publish: membuat tag git dan mendorongnya; workflow .github/workflows/release.yml lalu
# membangun ulang binary di GitHub Actions dan menerbitkannya di halaman Releases.
#
#   scripts/release.sh v0.1.0             # build + checksum lokal saja (uji sebelum rilis)
#   scripts/release.sh v0.1.0 --publish   # + tag & push → GitHub Actions membuat rilis
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
	git tag -a "$tag" -m "ubt $tag"
	git push origin "$tag"
	echo
	echo "Tag $tag dikirim. GitHub Actions sedang membangun rilisnya:"
	echo "  https://github.com/arif-rachim/ubuntu-tool/actions/workflows/release.yml"
fi
