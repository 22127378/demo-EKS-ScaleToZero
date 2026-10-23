#!/usr/bin/env bash
# Usage: scripts/detect-arch.sh <image[:tag]>
# Prints exactly one value to stdout for the app chart's `arch`: auto | amd64 | arm64
#   auto          : the image has both amd64 and arm64 (Karpenter picks the cheapest node)
#   amd64 / arm64 : the image supports a single architecture -> pin that one
# If the manifest cannot be read, prints amd64 and warns on stderr (exit code stays 0).
set -uo pipefail

if [[ $# -ne 1 || "$1" == -h || "$1" == --help ]]; then
  echo "Usage: $0 <image[:tag]>" >&2
  exit 2
fi
image="$1"
[[ "$image" == *[:@]* && "${image##*/}" == *[:@]* ]] || image="${image}:latest"

warn() { echo "WARNING: $*" >&2; }
fallback() { warn "$1 -> defaulting to amd64"; echo amd64; exit 0; }

command -v jq >/dev/null || fallback "jq is not installed"

# Returns JSON: a single object ({architecture,os}) or a map {"linux/amd64": {...}, ...}
json=""
if command -v docker >/dev/null && docker buildx version >/dev/null 2>&1; then
  json=$(timeout 60 docker buildx imagetools inspect --format '{{json .Image}}' "$image" 2>/dev/null) || json=""
fi
if ! jq -e . >/dev/null 2>&1 <<<"$json"; then
  json=""
  if command -v crane >/dev/null; then
    cfg=$(timeout 60 crane config "$image" 2>/dev/null) && json=$(jq -c '{architecture, os}' <<<"$cfg" 2>/dev/null)
  fi
fi
jq -e . >/dev/null 2>&1 <<<"$json" || fallback "cannot read the manifest of '$image' (wrong name/tag, private registry not logged in, or no network)"

# Normalize into a list of linux architectures, ignoring attestation entries (unknown)
archs=$(jq -r '
  (if type=="object" and has("architecture") then [.] else [.[]] end)
  | map(select((.os // "linux")=="linux" and .architecture != null and .architecture != "unknown"))
  | map(.architecture) | unique | .[]' <<<"$json" 2>/dev/null)

has_amd64=0; has_arm64=0
while read -r a; do
  [[ "$a" == amd64 ]] && has_amd64=1
  [[ "$a" == arm64 ]] && has_arm64=1
done <<<"$archs"

echo "Linux architectures found in '$image': $(tr '\n' ' ' <<<"$archs")" >&2

if   (( has_amd64 && has_arm64 )); then echo auto
elif (( has_arm64 ));              then echo arm64
elif (( has_amd64 ));              then echo amd64
else fallback "the image has neither linux/amd64 nor linux/arm64"
fi
