#!/usr/bin/env bash
set -euo pipefail

readonly ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SRC_DIR="${ROOT_DIR}/src"
readonly DIST_DIR="${ROOT_DIR}/dist"
readonly VERSION_PACKAGE="github.com/GMWalletApp/epusdt/config"
readonly SUPPORTED_TARGETS=(
  "linux-amd64"
  "linux-arm64"
  "linux-armv7"
  "darwin-amd64"
  "darwin-arm64"
  "windows-amd64"
  "windows-arm64"
)

log() {
  printf '[编译] %s\n' "$*"
}

die() {
  printf '[编译] 错误：%s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
用法：
  ./build.sh                         编译当前系统和架构
  ./build.sh linux-amd64             编译指定目标
  ./build.sh linux-amd64 darwin-arm64
  ./build.sh all                     编译全部支持目标

支持的目标：
  linux-amd64、linux-arm64、linux-armv7
  darwin-amd64、darwin-arm64
  windows-amd64、windows-arm64

可选环境变量：
  BUILD_VERSION=v1.2.3 ./build.sh linux-amd64
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"
}

current_target() {
  local os arch

  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    MINGW*|MSYS*|CYGWIN*) os="windows" ;;
    *) die "不支持当前操作系统：$(uname -s)" ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    armv7l|armv7) arch="armv7" ;;
    *) die "不支持当前处理器架构：$(uname -m)" ;;
  esac

  printf '%s-%s\n' "${os}" "${arch}"
}

target_supported() {
  local target="$1"
  local supported

  for supported in "${SUPPORTED_TARGETS[@]}"; do
    if [[ "${target}" == "${supported}" ]]; then
      return 0
    fi
  done
  return 1
}

safe_version_name() {
  printf '%s' "$1" | sed 's/[^A-Za-z0-9._-]/-/g'
}

write_checksum() {
  local archive_name="$1"

  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${DIST_DIR}" && sha256sum "${archive_name}" > "${archive_name}.sha256")
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    (cd "${DIST_DIR}" && shasum -a 256 "${archive_name}" > "${archive_name}.sha256")
    return
  fi
  die "缺少 SHA-256 校验工具，请安装 sha256sum 或 shasum"
}

build_target() {
  local target="$1"
  local goos="${target%%-*}"
  local goarch="${target#*-}"
  local goarm=""
  local binary_name="epusdt"
  local package_name="epusdt-${SAFE_VERSION}-${target}"
  local package_dir="${DIST_DIR}/${package_name}"
  local archive_name
  local ldflags

  if [[ "${goarch}" == "armv7" ]]; then
    goarch="arm"
    goarm="7"
  fi
  if [[ "${goos}" == "windows" ]]; then
    binary_name="epusdt.exe"
    archive_name="${package_name}.zip"
    require_command zip
  else
    archive_name="${package_name}.tar.gz"
    require_command tar
  fi

  rm -rf "${package_dir}"
  rm -f "${DIST_DIR}/${archive_name}" "${DIST_DIR}/${archive_name}.sha256"
  mkdir -p "${package_dir}"

  ldflags="-s -w -X ${VERSION_PACKAGE}.BuildVersion=${BUILD_VERSION_VALUE} -X ${VERSION_PACKAGE}.BuildCommit=${BUILD_COMMIT} -X ${VERSION_PACKAGE}.BuildDate=${BUILD_DATE}"
  log "正在编译 ${target}，版本 ${BUILD_VERSION_VALUE}"

  if [[ -n "${goarm}" ]]; then
    (cd "${SRC_DIR}" && CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" GOARM="${goarm}" go build --trimpath -ldflags "${ldflags}" -o "${package_dir}/${binary_name}" .)
  else
    (cd "${SRC_DIR}" && CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build --trimpath -ldflags "${ldflags}" -o "${package_dir}/${binary_name}" .)
  fi

  cp "${SRC_DIR}/.env.example" "${package_dir}/.env.example"
  [[ -s "${package_dir}/${binary_name}" ]] || die "编译产物为空：${package_dir}/${binary_name}"
  go version -m "${package_dir}/${binary_name}" >/dev/null

  if [[ "${goos}" == "windows" ]]; then
    (cd "${DIST_DIR}" && zip -qr "${archive_name}" "${package_name}")
  else
    (cd "${DIST_DIR}" && tar -czf "${archive_name}" "${package_name}")
  fi
  write_checksum "${archive_name}"

  log "编译完成：dist/${archive_name}"
  log "校验文件：dist/${archive_name}.sha256"
}

main() {
  local requested_targets=()
  local target

  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    usage
    return
  fi

  require_command go
  require_command git
  require_command sed

  BUILD_VERSION_VALUE="${BUILD_VERSION:-$(git -C "${ROOT_DIR}" describe --tags --always --dirty 2>/dev/null || printf '0.0.0-dev')}"
  BUILD_COMMIT="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || printf 'none')"
  BUILD_DATE="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  SAFE_VERSION="$(safe_version_name "${BUILD_VERSION_VALUE}")"
  [[ -n "${SAFE_VERSION}" ]] || die "构建版本不能为空"

  if [[ $# -eq 0 || "${1:-}" == "current" ]]; then
    requested_targets+=("$(current_target)")
  elif [[ "${1:-}" == "all" ]]; then
    requested_targets=("${SUPPORTED_TARGETS[@]}")
  else
    requested_targets=("$@")
  fi

  mkdir -p "${DIST_DIR}"
  for target in "${requested_targets[@]}"; do
    target_supported "${target}" || {
      usage >&2
      die "不支持的编译目标：${target}"
    }
    build_target "${target}"
  done
}

main "$@"
