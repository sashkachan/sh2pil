#!/bin/sh
# Build the release archives: one per supported target.
#
# Each archive holds the picker, its three helpers, the licence, the README, and the
# reference configuration. The helpers stay at helpers/ inside the archive, and the
# Homebrew formula installs each one into bin/ beside the binary.
#
# Usage: scripts/build-release.sh [version]
# With no argument the version comes from GITHUB_REF_NAME, minus a leading "v".
set -eu

version="${1:-${GITHUB_REF_NAME:-}}"
version="${version#v}"
if [ -z "$version" ]; then
    echo "build-release: no version given" >&2
    exit 2
fi

# sha256sum is Linux; macOS ships shasum. The release runs on Linux, and a local dry run
# needs the fallback.
sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$@"
    else
        shasum -a 256 "$@"
    fi
}

rm -rf dist
mkdir -p dist

for target in linux/amd64 darwin/arm64; do
    os="${target%/*}"
    arch="${target#*/}"
    name="sh2pil_${version}_${os}_${arch}"
    stage="dist/${name}"

    mkdir -p "${stage}/helpers"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
        -ldflags "-s -w -X main.version=${version}" -o "${stage}/sh2pil" .
    install -m 0755 helpers/sh2pil-sessions "${stage}/helpers/sh2pil-sessions"
    install -m 0755 helpers/sh2pil-open     "${stage}/helpers/sh2pil-open"
    install -m 0755 helpers/sh2pil-last     "${stage}/helpers/sh2pil-last"
    install -m 0644 LICENSE README.md config.example.yaml "${stage}/"

    tar -C dist -czf "dist/${name}.tar.gz" "${name}"
    rm -rf "${stage}"
done

(
    cd dist
    sha256 *.tar.gz > checksums.txt
)
cat dist/checksums.txt
