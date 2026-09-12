#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin

trajectory_version=${VERSION:-dev}
trajectory_commit=${COMMIT:-}
trajectory_build_date=${BUILD_DATE:-}

if [ -z "$trajectory_commit" ] && command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  trajectory_commit=$(git rev-parse --short=12 HEAD)
  if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
    trajectory_commit="${trajectory_commit}-dirty"
  fi
fi
if [ -z "$trajectory_build_date" ] && command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  trajectory_build_date=$(git show -s --format=%cI HEAD)
fi

trajectory_commit=${trajectory_commit:-unknown}
trajectory_build_date=${trajectory_build_date:-unknown}
trajectory_ldflags="-X main.version=${trajectory_version} -X main.commit=${trajectory_commit} -X main.buildDate=${trajectory_build_date}"

if [ -n "${MACOSX_DEPLOYMENT_TARGET:-}" ]; then
  CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
  CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
  export CGO_CFLAGS CGO_LDFLAGS
fi

"${GO:-go}" build -trimpath -buildvcs=false -ldflags "$trajectory_ldflags" -o bin/trajectory ./receiver
