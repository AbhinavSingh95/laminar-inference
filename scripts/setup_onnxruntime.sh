#!/usr/bin/env bash
#
# Download a pinned ONNX Runtime package for the optional ONNX backend.

set -euo pipefail

VERSION="${ONNXRUNTIME_VERSION:-1.24.4}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${ROOT_DIR}/third_party/onnxruntime"
TMP_DIR="${TMPDIR:-/tmp}/laminar-onnxruntime-${VERSION}"

os="$(uname -s)"
arch="$(uname -m)"

case "${os}-${arch}" in
  Darwin-arm64)
    package="onnxruntime-osx-arm64-${VERSION}.tgz"
    sha256="93787795f47e1eee369182e43ed51b9e5da0878ab0346aecf4258979b8bba989"
    library_name="libonnxruntime.dylib"
    ;;
  Linux-x86_64)
    package="onnxruntime-linux-x64-${VERSION}.tgz"
    sha256="3a211fbea252c1e66290658f1b735b772056149f28321e71c308942cdb54b747"
    library_name="libonnxruntime.so"
    ;;
  *)
    echo "Unsupported platform: ${os}-${arch}" >&2
    echo "Supported: Darwin-arm64, Linux-x86_64" >&2
    exit 1
    ;;
esac

url="https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/${package}"
archive="${TMP_DIR}/${package}"
extract_dir="${TMP_DIR}/extract"

mkdir -p "${TMP_DIR}" "${extract_dir}" "${INSTALL_DIR}/lib"

if [[ ! -f "${archive}" ]]; then
  echo "Downloading ${url}"
  curl -fsSL "${url}" -o "${archive}"
fi

actual_sha256="$(shasum -a 256 "${archive}" | awk '{print $1}')"
if [[ "${actual_sha256}" != "${sha256}" ]]; then
  echo "Checksum mismatch for ${archive}" >&2
  echo "expected: ${sha256}" >&2
  echo "actual:   ${actual_sha256}" >&2
  exit 1
fi

rm -rf "${extract_dir:?}"/*
tar -xzf "${archive}" -C "${extract_dir}"

package_dir="$(find "${extract_dir}" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
if [[ -z "${package_dir}" ]]; then
  echo "Could not find extracted ONNX Runtime package directory" >&2
  exit 1
fi

cp "${package_dir}/lib/${library_name}" "${INSTALL_DIR}/lib/${library_name}"
if [[ -f "${package_dir}/lib/libonnxruntime.${VERSION}.dylib" ]]; then
  cp "${package_dir}/lib/libonnxruntime.${VERSION}.dylib" "${INSTALL_DIR}/lib/"
fi
if [[ -f "${package_dir}/lib/libonnxruntime.so.${VERSION}" ]]; then
  cp "${package_dir}/lib/libonnxruntime.so.${VERSION}" "${INSTALL_DIR}/lib/"
fi

echo "Installed ONNX Runtime ${VERSION} to ${INSTALL_DIR}/lib"
