#!/usr/bin/env bash
# Compose guard (SSC-019): the local stack publishes the API on a loopback
# address only. compose.yaml defaults API_PUBLISH to [::1]:8080, and an
# override may come from the environment or from .env.
#
# The guard does not read .env itself. It asks Compose for the configuration
# Compose will actually run (docker compose config --format json), so
# Compose's own precedence, quoting, export and separator rules apply, and no
# second parser can drift from them. Every published port of every service
# must then name a loopback host address: ::1 or an IPv4 loopback address
# (127.x.y.z). The api service must publish at least one port.
#
# Anything else is refused (exit 2) before compose is started. A missing
# docker or jq, a failed docker compose config and an answer that cannot be
# read are refusals too, never a pass.
#
# Compose reads .env in the repository root unless API_ENV_FILE names another
# env file (the tests do).
set -euo pipefail

cd "${0%/*}/.."

refuse() {
  echo "compose guard: $* (SSC-019)" >&2
  exit 2
}

command -v docker >/dev/null 2>&1 || refuse "docker is not on PATH, so the published address cannot be checked"
command -v jq >/dev/null 2>&1 || refuse "jq is not on PATH, so the published address cannot be checked"

args=(compose)
if [[ -n ${API_ENV_FILE:-} ]]; then
  args+=(--env-file "$API_ENV_FILE")
fi
if ! config=$(docker "${args[@]}" config --format json 2>&1); then
  refuse "docker compose config failed, so the published address cannot be checked: $config"
fi

# One line per published port: service, host address, published port.
if ! ports=$(jq -r '
    .services | to_entries[] | .key as $s
    | (.value.ports // [])[]
    | [$s, (.host_ip // ""), ((.published // "") | tostring)] | @tsv
  ' <<<"$config"); then
  refuse "the configuration from docker compose config could not be read"
fi

api=0
while IFS=$'\t' read -r service host published; do
  [[ -n $service ]] || continue
  [[ $service == api ]] && api=$((api + 1))
  if [[ $host != "::1" && ! $host =~ ^127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
    refuse "service $service publishes on \"${host:-every address}\", not on loopback; set API_PUBLISH to [::1]:PORT or 127.0.0.1:PORT"
  fi
  if [[ -z $published || $published == 0 ]]; then
    refuse "service $service publishes on $host without a fixed port"
  fi
done <<<"$ports"

((api > 0)) || refuse "the api service publishes no port"
echo "compose guard: every published port is on loopback ($api for the api service)"
