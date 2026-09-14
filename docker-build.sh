#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 0 ]]; then
  echo "Usage: ./docker-build.sh" >&2
  exit 1
fi

commit="$(git rev-parse --short HEAD)"
version="lite-${commit}"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

docker compose build \
  --build-arg "VERSION=${version}" \
  --build-arg "COMMIT=${commit}" \
  --build-arg "BUILD_DATE=${build_date}"
docker compose up -d --pull never
