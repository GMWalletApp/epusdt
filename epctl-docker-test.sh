#!/usr/bin/env bash
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly CONTAINER_NAME="epctl-systemd-test-$$"
readonly BASE_IMAGE="${EPCTL_DOCKER_IMAGE:-ubuntu:24.04}"

ACTIVE_LANG=""
POSITIONAL_ARGS=()

detect_default_language() {
  local raw normalized

  raw="${EPCTL_LANG:-}"
  if [[ -z "${raw}" ]]; then
    printf 'zh\n'
    return
  fi

  normalized="${raw,,}"
  case "${normalized}" in
    zh|zh-*|zh_*|cn|chs|cht|中文)
      printf 'zh\n'
      ;;
    en|en-*|en_*|english)
      printf 'en\n'
      ;;
    *)
      printf '%s\n' "${raw}"
      ;;
  esac
}

set_language() {
  local requested normalized fallback_lang

  requested="${1:-$(detect_default_language)}"
  normalized="${requested,,}"
  fallback_lang="${ACTIVE_LANG:-$(detect_default_language)}"
  case "${normalized}" in
    zh|zh-*|zh_*|cn|chs|cht|中文)
      ACTIVE_LANG="zh"
      ;;
    en|en-*|en_*|english)
      ACTIVE_LANG="en"
      ;;
    *)
      ACTIVE_LANG="${fallback_lang}"
      die "$(trf unsupported_language "${requested}")"
      ;;
  esac

  export EPCTL_LANG="${ACTIVE_LANG}"
}

trf() {
  local key="$1"
  shift || true
  local template

  case "${ACTIVE_LANG}:${key}" in
    en:usage) template='Usage:' ;;
    zh:usage) template='用法：' ;;
    en:example) template='Example:' ;;
    zh:example) template='示例：' ;;
    en:missing_dependency) template='missing dependency: %s' ;;
    zh:missing_dependency) template='缺少依赖：%s' ;;
    en:arg_requires_value) template='%s requires a value' ;;
    zh:arg_requires_value) template='%s 需要一个参数值' ;;
    en:unsupported_language) template='unsupported language: %s' ;;
    zh:unsupported_language) template='不支持的语言：%s' ;;
    en:unsupported_arch) template='unsupported architecture: %s' ;;
    zh:unsupported_arch) template='不支持的架构：%s' ;;
    en:test_failed_collecting) template='test failed; collecting container diagnostics' ;;
    zh:test_failed_collecting) template='测试失败，正在收集容器诊断信息' ;;
    en:install_tag_invalid) template='install tag must be a real GitHub release tag such as v1.0.6' ;;
    zh:install_tag_invalid) template='安装 tag 必须是真实的 GitHub release tag，例如 v1.0.6' ;;
    en:upgrade_tag_invalid) template='upgrade tag must be a real GitHub release tag such as v1.0.6' ;;
    zh:upgrade_tag_invalid) template='升级 tag 必须是真实的 GitHub release tag，例如 v1.0.6' ;;
    en:base_image) template='base image: %s' ;;
    zh:base_image) template='基础镜像：%s' ;;
    en:starting_container) template='starting the Ubuntu systemd test container directly from %s' ;;
    zh:starting_container) template='正在直接从 %s 启动 Ubuntu systemd 测试容器' ;;
    en:systemd_not_ready) template='systemd did not become ready inside the container' ;;
    zh:systemd_not_ready) template='容器内的 systemd 未能及时就绪' ;;
    en:self_installing) template='installing epctl into PATH' ;;
    zh:self_installing) template='正在将 epctl 安装到 PATH' ;;
    en:downloading_release) template='downloading release %s' ;;
    zh:downloading_release) template='正在下载 release %s' ;;
    en:installing_release) template='installing epusdt from release %s' ;;
    zh:installing_release) template='正在从 release %s 安装 epusdt' ;;
    en:waiting_assets) template='waiting for epusdt to start and release www assets' ;;
    zh:waiting_assets) template='正在等待 epusdt 启动并释放 www 静态文件' ;;
    en:upgrading_release) template='upgrading epusdt from %s to %s' ;;
    zh:upgrading_release) template='正在将 epusdt 从 %s 升级到 %s' ;;
    en:verifying_upgrade_skip_restart) template='verifying that `upgrade --no-restart` skips the restart and keeps the current process running' ;;
    zh:verifying_upgrade_skip_restart) template='正在验证 `upgrade --no-restart` 会跳过重启，并保持当前进程继续运行' ;;
    en:verifying_upgrade_default_restart) template='verifying that a non-interactive upgrade restarts the service by default' ;;
    zh:verifying_upgrade_default_restart) template='正在验证非交互升级默认会重启服务' ;;
    en:verifying_upgrade_prompt_skip) template='verifying that `upgrade --prompt-restart` can skip the restart when `n` is entered' ;;
    zh:verifying_upgrade_prompt_skip) template='正在验证 `upgrade --prompt-restart` 在输入 `n` 时会跳过重启' ;;
    en:verifying_upgrade_prompt_restart) template='verifying that `upgrade --prompt-restart` restarts the service by default when Enter is pressed' ;;
    zh:verifying_upgrade_prompt_restart) template='正在验证 `upgrade --prompt-restart` 在直接回车时默认会重启服务' ;;
    en:checking_init_password) template='checking init-password behavior' ;;
    zh:checking_init_password) template='正在检查 init-password 行为' ;;
    en:expected_second_failure) template='expected init-password to fail after the password was changed' ;;
    zh:expected_second_failure) template='密码修改后，init-password 本应失败，但它却成功了' ;;
    en:verification_succeeded) template='docker verification succeeded for install=%s%s' ;;
    zh:verification_succeeded) template='Docker 验证通过，install=%s%s' ;;
    *)
      template="missing translation: ${key}"
      ;;
  esac

  printf -- "${template}" "$@"
}

log() {
  printf '[epctl-docker-test] %s\n' "$*" >&2
}

die() {
  printf '[epctl-docker-test] error: %s\n' "$*" >&2
  exit 1
}

usage() {
  if [[ "${ACTIVE_LANG}" == "zh" ]]; then
    cat <<'EOF'
用法：
  ./epctl-docker-test.sh [--lang zh|en] <install-tag> [upgrade-tag]

示例：
  ./epctl-docker-test.sh v1.0.6
  ./epctl-docker-test.sh --lang zh v1.0.6 v1.0.8
EOF
  else
    cat <<'EOF'
Usage:
  ./epctl-docker-test.sh [--lang zh|en] <install-tag> [upgrade-tag]

Example:
  ./epctl-docker-test.sh v1.0.6
  ./epctl-docker-test.sh --lang en v1.0.6 v1.0.8
EOF
  fi
}

parse_global_options() {
  POSITIONAL_ARGS=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --lang)
        [[ $# -ge 2 ]] || die "$(trf arg_requires_value "--lang")"
        set_language "$2"
        shift 2
        ;;
      --lang=*)
        set_language "${1#*=}"
        shift
        ;;
      *)
        POSITIONAL_ARGS+=("$1")
        shift
        ;;
    esac
  done
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "$(trf missing_dependency "$1")"
}

map_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      printf 'amd64\n'
      ;;
    aarch64|arm64)
      printf 'arm64\n'
      ;;
    *)
      die "$(trf unsupported_arch "$(uname -m)")"
      ;;
  esac
}

docker_bash() {
  docker exec "${CONTAINER_NAME}" bash -euo pipefail -c "$1"
}

cleanup() {
  local exit_code="$?"
  if [[ "${exit_code}" -ne 0 ]]; then
    log "$(trf test_failed_collecting)"
    docker logs "${CONTAINER_NAME}" >/dev/null 2>&1 && docker logs "${CONTAINER_NAME}" >&2 || true
    docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'systemctl status epusdt --no-pager || true' || true
    docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'journalctl -u epusdt --no-pager -n 100 || true' || true
  fi
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}

wait_for_systemd() {
  local attempt
  for attempt in $(seq 1 120); do
    if docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'systemctl show --property=Version --value >/dev/null 2>&1'; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_for_service_assets() {
  docker exec "${CONTAINER_NAME}" bash -euo pipefail -c '
for _ in $(seq 1 30); do
  if systemctl is-active --quiet epusdt && [[ -f /opt/epusdt/www/index.html ]]; then
    exit 0
  fi
  sleep 1
done
systemctl status epusdt --no-pager || true
journalctl -u epusdt --no-pager -n 100 || true
exit 1
'
}

service_main_pid() {
  docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'systemctl show --property=MainPID --value epusdt' | tr -d '\r\n'
}

set_language ""
parse_global_options "$@"

INSTALL_TAG="${POSITIONAL_ARGS[0]:-}"
UPGRADE_TAG="${POSITIONAL_ARGS[1]:-}"
[[ -n "${INSTALL_TAG}" ]] || {
  usage
  exit 1
}
[[ "${INSTALL_TAG}" == v* ]] || die "$(trf install_tag_invalid)"
if [[ -n "${UPGRADE_TAG}" && "${UPGRADE_TAG}" != v* ]]; then
  die "$(trf upgrade_tag_invalid)"
fi

require_command docker
require_command curl

ASSET_ARCH="$(map_arch)"
ASSET_URL="https://github.com/GMWalletApp/epusdt/releases/download/${INSTALL_TAG}/epusdt-${INSTALL_TAG#v}-linux-${ASSET_ARCH}.tar.gz"
SUMS_URL="https://github.com/GMWalletApp/epusdt/releases/download/${INSTALL_TAG}/SHA256SUMS"

curl --fail --silent --show-error --head "${ASSET_URL}" >/dev/null
curl --fail --silent --show-error --head "${SUMS_URL}" >/dev/null
if [[ -n "${UPGRADE_TAG}" ]]; then
  curl --fail --silent --show-error --head "https://github.com/GMWalletApp/epusdt/releases/download/${UPGRADE_TAG}/epusdt-${UPGRADE_TAG#v}-linux-${ASSET_ARCH}.tar.gz" >/dev/null
  curl --fail --silent --show-error --head "https://github.com/GMWalletApp/epusdt/releases/download/${UPGRADE_TAG}/SHA256SUMS" >/dev/null
fi

trap cleanup EXIT

log "$(trf base_image "${BASE_IMAGE}")"
log "$(trf starting_container "${BASE_IMAGE}")"
docker run -d \
  --name "${CONTAINER_NAME}" \
  --privileged \
  --cgroupns=host \
  -e container=docker \
  --tmpfs /run \
  --tmpfs /run/lock \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -v "${SCRIPT_DIR}:/work" \
  -w /work \
  "${BASE_IMAGE}" \
  bash -euo pipefail -c '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y \
      ca-certificates \
      curl \
      expect \
      jq \
      sudo \
      systemd \
      systemd-sysv \
      tar
    apt-get clean
    rm -rf /var/lib/apt/lists/*
    if ! id -u tester >/dev/null 2>&1; then
      useradd -m -s /bin/bash tester
    fi
    mkdir -p /etc/sudoers.d
    echo "tester ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/tester
    chmod 0440 /etc/sudoers.d/tester
    exec /sbin/init
  ' >/dev/null

wait_for_systemd || die "$(trf systemd_not_ready)"

log "$(trf self_installing)"
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c "su - tester -c 'cd /work && ./epctl self-install'"
docker_bash '[[ -x /usr/local/bin/epctl ]]'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'su - tester -c "EPCTL_NO_COLOR=1 epctl help | grep -q \"^用法：\""'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'su - tester -c "EPCTL_NO_COLOR=1 epctl --lang zh help | grep -q \"^用法：\""'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'su - tester -c "EPCTL_NO_COLOR=1 epctl --lang en help | grep -q \"^Usage:\""'

log "$(trf downloading_release "${INSTALL_TAG}")"
docker exec "${CONTAINER_NAME}" env TAG="${INSTALL_TAG}" bash -euo pipefail -c 'su - tester -c "epctl download --tag ${TAG}"'

log "$(trf installing_release "${INSTALL_TAG}")"
docker exec "${CONTAINER_NAME}" env TAG="${INSTALL_TAG}" bash -euo pipefail -c 'su - tester -c "epctl install --tag ${TAG} --app-uri http://127.0.0.1:8000"'

log "$(trf waiting_assets)"
wait_for_service_assets

docker_bash '[[ -x /opt/epusdt/epusdt ]]'
docker_bash '[[ -f /opt/epusdt/.env ]]'
docker_bash 'systemctl is-active --quiet epusdt'
docker_bash '[[ -f /opt/epusdt/www/index.html ]]'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'su - tester -c "epctl show-config | grep -q \"^install=false$\""'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'su - tester -c "EPCTL_NO_COLOR=1 epctl --lang zh show-config | grep -q \"^install=false$\""'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c '
su - tester -c "epctl status >/tmp/epctl-status.out 2>/tmp/epctl-status.err"
! grep -q "systemd-journal" /tmp/epctl-status.err
'
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c '
su - tester -c "epctl logs --lines 50 >/tmp/epctl-logs.out 2>/tmp/epctl-logs.err"
! grep -q "systemd-journal" /tmp/epctl-logs.err
'

if [[ -n "${UPGRADE_TAG}" ]]; then
  log "$(trf upgrading_release "${INSTALL_TAG}" "${UPGRADE_TAG}")"
  env_checksum_before="$(docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'sha256sum /opt/epusdt/.env | cut -d" " -f1' | tr -d '\r\n')"
  pid_before_skip_restart="$(service_main_pid)"

  log "$(trf verifying_upgrade_skip_restart)"
  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c '
output_file="$(mktemp)"
su - tester -c "EPCTL_NO_COLOR=1 epctl --lang en upgrade --tag ${TAG} --no-restart" >"${output_file}" 2>&1
grep -q "systemctl restart epusdt" "${output_file}"
! grep -q "Restart .*\[Y/n\]" "${output_file}"
rm -f "${output_file}"
'
  pid_after_skip_restart="$(service_main_pid)"
  [[ "${pid_before_skip_restart}" == "${pid_after_skip_restart}" ]]
  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c '/opt/epusdt/epusdt version | grep -q "^version: ${TAG}$"'
  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c 'cmp -s /opt/epusdt/.env.example "/tmp/epusdt/${TAG}/extract/.env.example"'
  env_checksum_after_skip="$(docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'sha256sum /opt/epusdt/.env | cut -d" " -f1' | tr -d '\r\n')"
  [[ "${env_checksum_before}" == "${env_checksum_after_skip}" ]]

  log "$(trf verifying_upgrade_default_restart)"
  pid_before_default_restart="$(service_main_pid)"
  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c '
output_file="$(mktemp)"
su - tester -c "EPCTL_NO_COLOR=1 epctl --lang en upgrade --tag ${TAG}" >"${output_file}" 2>&1
! grep -q "Restart .*\[Y/n\]" "${output_file}"
rm -f "${output_file}"
'
  wait_for_service_assets
  pid_after_default_restart="$(service_main_pid)"
  [[ "${pid_before_default_restart}" != "${pid_after_default_restart}" ]]

  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c '
output_file="$(mktemp)"
if su - tester -c "EPCTL_NO_COLOR=1 epctl --lang en upgrade --tag ${TAG} --prompt-restart" </dev/null >"${output_file}" 2>&1; then
  cat "${output_file}" >&2
  rm -f "${output_file}"
  exit 1
fi
grep -q "interactive terminal" "${output_file}"
rm -f "${output_file}"
'

  log "$(trf verifying_upgrade_prompt_skip)"
  pid_before_prompt_skip="$(service_main_pid)"
  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c '
output_file="$(mktemp)"
OUTPUT_FILE="${output_file}" expect <<'"'"'EOF'"'"'
set timeout 120
log_file -noappend $env(OUTPUT_FILE)
spawn su - tester -c "EPCTL_NO_COLOR=1 epctl --lang en upgrade --tag $env(TAG) --prompt-restart"
expect {
  -re {Restart .*\[Y/n\]} { send "n\r" }
  timeout { exit 1 }
}
expect eof
EOF
grep -q "systemctl restart epusdt" "${output_file}"
rm -f "${output_file}"
'
  pid_after_prompt_skip="$(service_main_pid)"
  [[ "${pid_before_prompt_skip}" == "${pid_after_prompt_skip}" ]]

  log "$(trf verifying_upgrade_prompt_restart)"
  pid_before_prompt_restart="$(service_main_pid)"
  docker exec "${CONTAINER_NAME}" env TAG="${UPGRADE_TAG}" bash -euo pipefail -c '
expect <<'"'"'EOF'"'"'
set timeout 120
spawn su - tester -c "EPCTL_NO_COLOR=1 epctl --lang en upgrade --tag $env(TAG) --prompt-restart"
expect {
  -re {Restart .*\[Y/n\]} { send "\r" }
  timeout { exit 1 }
}
expect eof
EOF
'
  wait_for_service_assets
  pid_after_prompt_restart="$(service_main_pid)"
  [[ "${pid_before_prompt_restart}" != "${pid_after_prompt_restart}" ]]

  docker exec "${CONTAINER_NAME}" bash -euo pipefail -c 'su - tester -c "epctl show-config | grep -q \"^install=false$\""'
  docker exec "${CONTAINER_NAME}" bash -euo pipefail -c '
su - tester -c "epctl status >/tmp/epctl-status-upgrade.out 2>/tmp/epctl-status-upgrade.err"
! grep -q "systemd-journal" /tmp/epctl-status-upgrade.err
'
fi

log "$(trf checking_init_password)"
docker exec "${CONTAINER_NAME}" bash -euo pipefail -c '
response="$(su - tester -c "epctl init-password")"
password="$(printf "%s\n" "${response}" | jq -r ".data.password")"
printf "%s\n" "${response}" | jq -e ".status_code == 200 and (.data.password | type == \"string\" and length > 0)" >/dev/null

login_payload="$(jq -nc --arg username admin --arg password "${password}" "{username:\$username,password:\$password}")"
token="$(curl --fail --silent --show-error \
  -H "Content-Type: application/json" \
  -d "${login_payload}" \
  http://127.0.0.1:8000/admin/api/v1/auth/login | jq -r ".data.token")"
[[ -n "${token}" && "${token}" != "null" ]]

change_payload="$(jq -nc --arg old_password "${password}" --arg new_password "new-pass-789" "{old_password:\$old_password,new_password:\$new_password}")"
curl --fail --silent --show-error \
  -H "Authorization: Bearer ${token}" \
  -H "Content-Type: application/json" \
  -d "${change_payload}" \
  http://127.0.0.1:8000/admin/api/v1/auth/password | jq -e ".status_code == 200" >/dev/null

failure_output="$(mktemp)"
if su - tester -c "epctl init-password" >"${failure_output}" 2>&1; then
  cat "${failure_output}" >&2
  rm -f "${failure_output}"
  echo "'"$(trf expected_second_failure)"'" >&2
  exit 1
fi
grep -Eq "\"status_code\"[[:space:]]*:[[:space:]]*10040" "${failure_output}"
rm -f "${failure_output}"
'

log "$(trf verification_succeeded "${INSTALL_TAG}" "${UPGRADE_TAG:+ upgrade=${UPGRADE_TAG}}")"
