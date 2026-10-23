#!/usr/bin/env bash
# Build the demo images (api, worker, web) for linux/amd64 and linux/arm64.
#
# Usage:
#   scripts/build-images.sh                      # build locally only, tag demo-<name>:<tag>
#   scripts/build-images.sh <registry> [--push]  # tag <registry>/demo-<name>:<tag>, optionally push
#
# Without --push the images are loaded into the local image store, which holds
# every requested platform only when the containerd image store is enabled.
#
# Environment:
#   TAG        image tag (default: dev)
#   PLATFORMS  target platforms (default: linux/amd64,linux/arm64)
#   ONLY       space-separated subset of "api worker web" (default: all three)
#
# Multi-platform output needs the containerd image store or a docker-container
# buildx builder; the Go and Node stages cross-compile natively, so no QEMU is needed.
set -euo pipefail

registry=""
push=0
for arg in "$@"; do
  case "$arg" in
    --push) push=1 ;;
    -*)     echo "ERROR: unknown option '$arg'" >&2; exit 2 ;;
    *)      registry="$arg" ;;
  esac
done
if (( push )) && [[ -z "$registry" ]]; then
  echo "ERROR: --push requires a registry argument" >&2
  exit 2
fi

tag="${TAG:-dev}"
platforms="${PLATFORMS:-linux/amd64,linux/arm64}"
only="${ONLY:-api worker web}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for name in $only; do
  case "$name" in
    api|worker) dockerfile="$root/src/$name/Dockerfile"; context="$root/src" ;;
    web)        dockerfile="$root/src/web/Dockerfile";   context="$root/src/web" ;;
    *) echo "ERROR: unknown image '$name' (expected api, worker or web)" >&2; exit 2 ;;
  esac
  image="demo-$name:$tag"
  [[ -n "$registry" ]] && image="$registry/$image"

  args=(buildx build --platform "$platforms" -f "$dockerfile" -t "$image")
  if (( push )); then args+=(--push); else args+=(--load); fi

  echo "==> $image ($platforms)"
  docker "${args[@]}" "$context"
done
