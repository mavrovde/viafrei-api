#!/usr/bin/env bash
# Compose guard (SSC-019): the local stack publishes the API on a loopback
# address only. compose.yaml defaults API_PUBLISH to [::1]:8080; an override,
# from the environment or from .env (the environment wins, as in compose),
# must name [::1] or an IPv4 loopback address (127.x.y.z) followed by a port. Anything
# else is refused before compose is started.
#
# A line of the env file may start with "export ", as Compose accepts.
# The env file is .env in the repository root unless API_ENV_FILE names
# another one (the tests do).
set -euo pipefail

cd "$(dirname "$0")/.."

env_file="${API_ENV_FILE:-.env}"
value=""
source="default"
if [[ -n "${API_PUBLISH+x}" ]]; then
  value="$API_PUBLISH"
  source="environment"
elif [[ -f $env_file ]]; then
  line=$(grep -E '^[[:space:]]*(export[[:space:]]+)?API_PUBLISH=' "$env_file" | tail -n 1 || true)
  if [[ -n $line ]]; then
    value="${line#*=}"
    value="${value%%#*}"
    value="${value%"${value##*[![:space:]]}"}"
    value="${value#\"}"; value="${value%\"}"
    value="${value#\'}"; value="${value%\'}"
    source="$env_file"
  fi
fi

if [[ -z $value ]]; then
  echo "compose guard: API_PUBLISH from the $source is the loopback default"
  exit 0
fi

port='(6553[0-5]|655[0-2][0-9]|65[0-4][0-9]{2}|6[0-4][0-9]{3}|[1-5][0-9]{4}|[1-9][0-9]{0,3})'
octet='(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])'
if [[ $value =~ ^\[::1\]:${port}$ ]] || [[ $value =~ ^127\.${octet}\.${octet}\.${octet}:${port}$ ]]; then
  echo "compose guard: API_PUBLISH from the $source is on loopback"
  exit 0
fi

echo "compose guard: API_PUBLISH from the $source (\"$value\") is not a loopback address and port; use [::1]:PORT or 127.0.0.1:PORT (SSC-019)" >&2
exit 2
