#!/bin/sh
set -eu

repository="${CASSIE_REPOSITORY:-andocodes/cassie}"
install_dir="${CASSIE_INSTALL_DIR:-$HOME/.local/bin}"
base_url="https://github.com/${repository}/releases/latest/download"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "cassie: unsupported operating system" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=x86_64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "cassie: unsupported architecture" >&2; exit 1 ;;
esac

archive="cassie_${os}_${arch}.tar.gz"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT INT TERM

curl --fail --location --silent --show-error "${base_url}/${archive}" --output "${temporary}/${archive}"
curl --fail --location --silent --show-error "${base_url}/checksums.txt" --output "${temporary}/checksums.txt"

expected="$(awk -v archive="$archive" '$2 == archive { print $1 }' "${temporary}/checksums.txt")"
if [ -z "$expected" ]; then
  echo "cassie: release checksum not found" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${temporary}/${archive}" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "${temporary}/${archive}" | awk '{ print $1 }')"
fi

if [ "$actual" != "$expected" ]; then
  echo "cassie: checksum verification failed" >&2
  exit 1
fi

tar -xzf "${temporary}/${archive}" -C "$temporary" cassie
mkdir -p "$install_dir"
install -m 0755 "${temporary}/cassie" "${install_dir}/cassie"
echo "Installed cassie to ${install_dir}/cassie"
