#!/usr/bin/env bash
# Benzhi docker build helper (standard template, do not change logic).
set -euo pipefail

NAME="${1:?usage: build_benzhi_docker.sh <name> <platform>}"
PLATFORM="${2:-linux/amd64}"

IMAGE="benzhi/${NAME}:latest"

docker buildx build \
  --platform "${PLATFORM}" \
  -f benzhi.Dockerfile \
  -t "${IMAGE}" \
  --load \
  .

echo "built ${IMAGE} for ${PLATFORM}"
