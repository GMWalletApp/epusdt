#!/bin/sh
set -eu

escape_sed_replacement() {
  printf '%s' "$1" | sed -e 's/[&|\\]/\\&/g'
}

set_env_key() {
  file="$1"
  key="$2"
  value="$3"
  escaped="$(escape_sed_replacement "${value}")"
  if grep -q "^${key}=" "${file}" 2>/dev/null; then
    sed -i "s|^${key}=.*$|${key}=${escaped}|" "${file}"
  else
    printf '%s=%s\n' "${key}" "${value}" >> "${file}"
  fi
}

render_chainscanner_config() {
  config_file="$1"
  listen_addr="${CHAINSCANNER_LISTEN:-0.0.0.0:9081}"
  db_path="${CHAINSCANNER_DB:-/app/runtime/chainscanner.db}"
  scanner_token="${CHAINSCANNER_TOKEN:-epusdt_internal_scanner_token_9f8e7d6c5b4a}"
  admin_token="${CHAINSCANNER_ADMIN_TOKEN:-epusdt_internal_admin_token_1a2b3c4d5e6f}"

  mkdir -p "$(dirname "${config_file}")" "$(dirname "${db_path}")"

  if [ ! -f "${config_file}" ] || [ "${CHAINSCANNER_OVERWRITE_CONFIG:-false}" = "true" ]; then
    cat > "${config_file}" <<EOF
{
  "listen": "${listen_addr}",
  "database": "${db_path}",
  "networks": [
    {"name": "ethereum"},
    {"name": "binance"},
    {"name": "polygon"},
    {"name": "plasma"},
    {"name": "tron"},
    {"name": "ton"},
    {"name": "aptos"},
    {"name": "solana"},
    {"name": "sui"},
    {"name": "arbitrum"},
    {"name": "hyperevm"},
    {"name": "monad"},
    {"name": "arc"},
    {"name": "avalanche"},
    {"name": "optimism"},
    {"name": "base"}
  ],
  "consumers": {
    "epusdt": "${scanner_token}"
  },
  "admin_token": "${admin_token}"
}
EOF
    chmod 0600 "${config_file}" 2>/dev/null || true
  fi
}

prepare_epusdt_config() {
  target_env="${EPUSDT_CONFIG:-/app/.env}"
  mkdir -p "$(dirname "${target_env}")"
  if [ -d /app ]; then
    mkdir -p /app/runtime 2>/dev/null || true
  fi

  if [ ! -f "${target_env}" ]; then
    if [ -f /app/.env.example ]; then
      cp /app/.env.example "${target_env}"
    elif [ -f /app/.env ]; then
      cp /app/.env "${target_env}"
    else
      touch "${target_env}"
    fi
  fi

  if [ "${target_env}" = "/app/runtime/.env" ]; then
    set_env_key "${target_env}" "runtime_root_path" "/app/runtime"
    set_env_key "${target_env}" "sqlite_database_filename" "/app/runtime/epusdt.db"
  fi

  if [ -n "${EPUSDT_INSTALL:-}" ]; then
    set_env_key "${target_env}" "install" "${EPUSDT_INSTALL}"
  fi
  if [ -n "${APP_URI:-}" ]; then
    set_env_key "${target_env}" "app_uri" "${APP_URI}"
  fi
  if [ -n "${HTTP_LISTEN:-}" ]; then
    set_env_key "${target_env}" "http_listen" "${HTTP_LISTEN}"
  fi
  if [ -n "${API_RATE_URL:-}" ]; then
    set_env_key "${target_env}" "api_rate_url" "${API_RATE_URL}"
  fi
  if [ -n "${CHAINSCANNER_URL:-}" ]; then
    set_env_key "${target_env}" "chainscanner_url" "${CHAINSCANNER_URL}"
  fi
  if [ -n "${CHAINSCANNER_TOKEN:-}" ]; then
    set_env_key "${target_env}" "chainscanner_token" "${CHAINSCANNER_TOKEN}"
  fi
  if [ -n "${CHAINSCANNER_ADMIN_TOKEN:-}" ]; then
    set_env_key "${target_env}" "chainscanner_admin_token" "${CHAINSCANNER_ADMIN_TOKEN}"
  fi
  if [ -n "${CHAINSCANNER_TRANSPORT:-}" ]; then
    set_env_key "${target_env}" "chainscanner_transport" "${CHAINSCANNER_TRANSPORT}"
  fi
}

if [ "$#" -eq 0 ]; then
  set -- ./epusdt http start
fi

case "$1" in
  chainscanner|*/chainscanner)
    cmd_bin="$1"
    if [ "${cmd_bin}" = "chainscanner" ] && [ -x /app/chainscanner ]; then
      cmd_bin="/app/chainscanner"
    fi
    config_file="${CHAINSCANNER_CONFIG:-/app/runtime/chainscanner.json}"
    prev=""
    for arg in "$@"; do
      if [ "${prev}" = "-config" ] || [ "${prev}" = "--config" ]; then
        config_file="${arg}"
        break
      fi
      case "${arg}" in
        -config=*|--config=*)
          config_file="${arg#*=}"
          break
          ;;
      esac
      prev="${arg}"
    done
    render_chainscanner_config "${config_file}"
    if [ "$#" -eq 1 ]; then
      exec "${cmd_bin}" -config "${config_file}"
    fi
    exec "$@"
    ;;
  epusdt|*/epusdt)
    prepare_epusdt_config
    exec "$@"
    ;;
  *)
    exec "$@"
    ;;
esac
