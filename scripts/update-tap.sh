#!/bin/sh
# Render the Homebrew formula for one release and push it to the tap repository.
#
# The caller skips this script when TAP_TOKEN is empty, so a release still succeeds when the
# tap is not configured. TAP_TOKEN must be a token that can push to the tap repository.
#
# Usage: scripts/update-tap.sh <tag>
# Run scripts/build-release.sh first: this reads the checksums from dist/checksums.txt.
set -eu

tag="${1:?usage: update-tap.sh <tag>}"
version="${tag#v}"
repo="${GITHUB_REPOSITORY:-sashkachan/sh2pil}"
tap="${TAP_REPOSITORY:-sashkachan/homebrew-tap}"

if [ -z "${TAP_TOKEN:-}" ]; then
    echo "update-tap: TAP_TOKEN is not set" >&2
    exit 2
fi

# sum prints the checksum of one archive. The tools write a bare file name, and a leading
# "./" is stripped in case one is ever added, because the formula needs an exact match.
sum() {
    awk -v name="$1" '{ n = $2; sub(/^\.\//, "", n); if (n == name) print $1 }' dist/checksums.txt
}

darwin_sha=$(sum "sh2pil_${version}_darwin_arm64.tar.gz")
linux_sha=$(sum "sh2pil_${version}_linux_amd64.tar.gz")
if [ -z "$darwin_sha" ] || [ -z "$linux_sha" ]; then
    echo "update-tap: checksums for ${version} are missing; run scripts/build-release.sh first" >&2
    exit 1
fi

base="https://github.com/${repo}/releases/download/${tag}"
work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT

git clone --quiet --depth 1 "https://github.com/${tap}.git" "${work}/tap"
mkdir -p "${work}/tap/Formula"

cat > "${work}/tap/Formula/sh2pil.rb" <<EOF
class Sh2pil < Formula
  desc "Terminal session picker for Pi, OpenCode, Claude Code, and Codex"
  homepage "https://github.com/${repo}"
  version "${version}"
  license "MIT"

  on_macos do
    on_arm do
      url "${base}/sh2pil_${version}_darwin_arm64.tar.gz"
      sha256 "${darwin_sha}"
    end
  end

  on_linux do
    on_intel do
      url "${base}/sh2pil_${version}_linux_amd64.tar.gz"
      sha256 "${linux_sha}"
    end
  end

  def install
    bin.install "sh2pil"
    bin.install "helpers/pib" => "pib"
    bin.install "helpers/pib-open" => "pib-open"
    bin.install "helpers/pi-last" => "pi-last"
    (share/"sh2pil").install "config.example.yaml"
  end

  test do
    assert_match "sh2pil", shell_output("#{bin}/sh2pil --version")
  end
end
EOF

cd "${work}/tap"
git add Formula/sh2pil.rb
if git diff --cached --quiet; then
    echo "update-tap: Formula/sh2pil.rb is already at ${version}"
    exit 0
fi

git -c user.name="sh2pil release" \
    -c user.email="sashkachan@users.noreply.github.com" \
    commit --quiet -m "sh2pil ${version}"
git -c http.extraheader="AUTHORIZATION: bearer ${TAP_TOKEN}" push --quiet origin HEAD:main
echo "update-tap: pushed sh2pil ${version} to ${tap}"
