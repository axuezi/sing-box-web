#!/bin/sh
# Builds sing-box from an official release tag with the official build tags
# plus with_v2ray_api (per-user traffic needs it; official builds leave it
# out) and packs one archive per architecture for the GitHub release:
#   <out>/sing-box-v2api_linux_<arch>.tar.gz  (sing-box, LICENSE, SOURCE)
# Usage: scripts/build-sing-box.sh v1.14.3 dist-singbox
set -eu
tag=$1
out=$(mkdir -p "$2" && cd "$2" && pwd)
src=$(mktemp -d)
trap 'rm -rf "$src"' EXIT

git clone --quiet --depth 1 --branch "$tag" https://github.com/SagerNet/sing-box.git "$src"
cd "$src"
commit=$(git rev-parse HEAD)
tags="$(cat release/DEFAULT_BUILD_TAGS_OTHERS),with_v2ray_api"
version=$(go run ./cmd/internal/read_tag)
for arch in amd64 arm64; do
  stage="$src/stage-$arch"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -tags "$tags" \
    -ldflags "-X \"github.com/sagernet/sing-box/constant.Version=$version\" $(cat release/LDFLAGS) -s -w -buildid=" \
    -o "$stage/sing-box" ./cmd/sing-box
  cp LICENSE "$stage/"
  printf 'sing-box %s built from https://github.com/SagerNet/sing-box (tag %s, commit %s)\nbuild tags: %s\n' \
    "$version" "$tag" "$commit" "$tags" > "$stage/SOURCE"
  tar -C "$stage" -czf "$out/sing-box-v2api_linux_$arch.tar.gz" sing-box LICENSE SOURCE
done
ls -l "$out"
