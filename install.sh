#!/bin/sh
set -efu

trajectory_is_semver() {
  trajectory_semver=${1#v}
  if ! printf '%s\n' "$trajectory_semver" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
    return 1
  fi

  trajectory_without_build=${trajectory_semver%%+*}
  case "$trajectory_without_build" in
    *-*) trajectory_prerelease=${trajectory_without_build#*-} ;;
    *) return 0 ;;
  esac

  trajectory_previous_ifs=$IFS
  IFS=.
  trajectory_prerelease_valid=1
  for trajectory_identifier in $trajectory_prerelease; do
    case "$trajectory_identifier" in
      *[!0-9]*) ;;
      0 | [1-9] | [1-9][0-9]*) ;;
      *) trajectory_prerelease_valid=0 ;;
    esac
  done
  IFS=$trajectory_previous_ifs
  [ "$trajectory_prerelease_valid" = 1 ]
}

if [ "${1:-}" = "--check-version" ]; then
  if [ "$#" -ne 2 ] || ! trajectory_is_semver "$2"; then
    echo "trajectory: invalid release version: ${2:-}" >&2
    exit 1
  fi
  exit 0
elif [ "$#" -ne 0 ]; then
  echo "trajectory: unexpected argument: $1" >&2
  exit 1
fi

trajectory_repository="asoules/trajectory"
trajectory_version=${TRAJECTORY_VERSION:-latest}
trajectory_install_dir=${TRAJECTORY_INSTALL_DIR:-"${HOME}/.local/bin"}

case "$(uname -s)" in
  Darwin) trajectory_os=darwin ;;
  Linux) trajectory_os=linux ;;
  *)
    echo "trajectory: unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  arm64 | aarch64) trajectory_arch=arm64 ;;
  x86_64 | amd64) trajectory_arch=amd64 ;;
  *)
    echo "trajectory: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if [ -n "${TRAJECTORY_DOWNLOAD_ROOT:-}" ]; then
  trajectory_download_root=${TRAJECTORY_DOWNLOAD_ROOT%/}
  trajectory_custom_download_root=1
else
  trajectory_custom_download_root=0
  case "$trajectory_version" in
    latest)
      trajectory_download_root="https://github.com/${trajectory_repository}/releases/latest/download"
      ;;
    *)
      if ! trajectory_is_semver "$trajectory_version"; then
        echo "trajectory: invalid TRAJECTORY_VERSION: ${trajectory_version}" >&2
        exit 1
      fi
      case "$trajectory_version" in
        v*) ;;
        *) trajectory_version="v${trajectory_version}" ;;
      esac
      trajectory_download_root="https://github.com/${trajectory_repository}/releases/download/${trajectory_version}"
      ;;
  esac
fi

trajectory_archive="trajectory_${trajectory_os}_${trajectory_arch}.tar.gz"
trajectory_tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/trajectory-install.XXXXXX")
trajectory_install_tmp=
trajectory_cleanup() {
  rm -rf "$trajectory_tmp_dir"
  if [ -n "$trajectory_install_tmp" ]; then
    rm -f "$trajectory_install_tmp"
  fi
}
trap trajectory_cleanup 0
trap 'exit 1' HUP INT TERM

if ! command -v curl >/dev/null 2>&1; then
  echo "trajectory: curl is required" >&2
  exit 1
fi

trajectory_download() {
  if [ "$trajectory_custom_download_root" = 1 ]; then
    curl -fsSL "$1" -o "$2"
  else
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "$1" -o "$2"
  fi
}

trajectory_download "${trajectory_download_root}/checksums.txt" "${trajectory_tmp_dir}/checksums.txt"
trajectory_download "${trajectory_download_root}/${trajectory_archive}" "${trajectory_tmp_dir}/${trajectory_archive}"

trajectory_expected=$(awk -v name="$trajectory_archive" '$2 == name { print $1; exit }' "${trajectory_tmp_dir}/checksums.txt")
if [ -z "$trajectory_expected" ]; then
  echo "trajectory: ${trajectory_archive} is missing from checksums.txt" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  trajectory_actual=$(sha256sum "${trajectory_tmp_dir}/${trajectory_archive}" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  trajectory_actual=$(shasum -a 256 "${trajectory_tmp_dir}/${trajectory_archive}" | awk '{ print $1 }')
else
  echo "trajectory: sha256sum or shasum is required" >&2
  exit 1
fi

if [ "$trajectory_actual" != "$trajectory_expected" ]; then
  echo "trajectory: checksum verification failed for ${trajectory_archive}" >&2
  exit 1
fi

tar -xOf "${trajectory_tmp_dir}/${trajectory_archive}" trajectory > "${trajectory_tmp_dir}/trajectory"
mkdir -p "$trajectory_install_dir"
trajectory_install_tmp=$(mktemp "${trajectory_install_dir}/.trajectory-install.XXXXXX")
install -m 0755 "${trajectory_tmp_dir}/trajectory" "$trajectory_install_tmp"

if ! trajectory_installed_version=$("$trajectory_install_tmp" version); then
  echo "trajectory: downloaded executable failed its version check" >&2
  exit 1
fi

mv -f "$trajectory_install_tmp" "${trajectory_install_dir}/trajectory"
trajectory_install_tmp=
printf 'Installed %s to %s\n' "$trajectory_installed_version" "${trajectory_install_dir}/trajectory"

case ":${PATH}:" in
  *":${trajectory_install_dir}:"*) ;;
  *) printf 'Add %s to PATH to run trajectory from any directory.\n' "$trajectory_install_dir" ;;
esac
