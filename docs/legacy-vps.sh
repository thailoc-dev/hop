#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: vps <command> [args]

Commands:
  tunnel <ssh-server> <docker-container> <remote-port> <local-port>
      Forward localhost:<local-port> to a Docker container port on the VPS.

  docker-ip <ssh-server> <docker-container>
      Print the Docker container IP on the VPS.

  run <ssh-server> <command...>
      Run a command on the VPS.

  shell <ssh-server>
      Open an interactive SSH shell on the VPS.

  push [scp-options...] <ssh-server> <local-path> <remote-path>
      Copy a local file or directory to the VPS.

  pull [scp-options...] <ssh-server> <remote-path> <local-path>
      Copy a file or directory from the VPS.

Examples:
  vps tunnel example-backend-dev app_mongo_staging 27017 27018
  vps run example-backend-dev docker ps
  vps shell example-backend-dev
  vps push example-backend-dev ./app.log /tmp/app.log
  vps push -r example-backend-dev ./dist /tmp/dist
  vps pull example-backend-dev /var/log/app.log ./app.log
  vps pull -r example-backend-dev /tmp/dist ./dist
USAGE
}

die_usage() {
  local message="$1"

  printf 'Error: %s\n\n' "$message" >&2
  usage
  exit 64
}

require_arg_count() {
  local actual="$1"
  local expected="$2"
  local command_name="$3"

  if [[ "$actual" -ne "$expected" ]]; then
    die_usage "\"$command_name\" expects $expected argument(s)."
  fi
}

require_min_arg_count() {
  local actual="$1"
  local minimum="$2"
  local command_name="$3"

  if [[ "$actual" -lt "$minimum" ]]; then
    die_usage "\"$command_name\" expects at least $minimum argument(s)."
  fi
}

split_scp_args() {
  local command_name="$1"
  shift

  require_min_arg_count "$#" 3 "$command_name"

  scp_options=()
  while [[ "$#" -gt 3 ]]; do
    scp_options+=("$1")
    shift
  done

  scp_server="$1"
  scp_source="$2"
  scp_dest="$3"
}

require_port() {
  local value="$1"
  local name="$2"

  if [[ ! "$value" =~ ^[0-9]+$ ]]; then
    die_usage "$name must be a number."
  fi
}

docker_ip() {
  local ssh_server="$1"
  local container="$2"
  local inspect_format container_ip

  inspect_format='{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}'

  if ! container_ip="$(ssh "$ssh_server" docker inspect -f "$inspect_format" "$container" 2>/dev/null)"; then
    printf 'Error: cannot inspect Docker container "%s" on SSH server "%s".\n' "$container" "$ssh_server" >&2
    exit 1
  fi

  container_ip="$(printf '%s' "$container_ip" | tr -d '[:space:]')"

  if [[ -z "$container_ip" ]]; then
    printf 'Error: Docker container "%s" has no network IP on SSH server "%s".\n' "$container" "$ssh_server" >&2
    exit 1
  fi

  printf '%s\n' "$container_ip"
}

if [[ $# -eq 0 ]]; then
  usage
  exit 64
fi

command_name="$1"
shift

case "$command_name" in
  tunnel)
    require_arg_count "$#" 4 "$command_name"

    ssh_server="$1"
    container="$2"
    remote_port="$3"
    local_port="$4"

    require_port "$remote_port" "remote-port"
    require_port "$local_port" "local-port"

    container_ip="$(docker_ip "$ssh_server" "$container")"

    printf 'Container IP: %s\n' "$container_ip"
    printf 'Forwarding localhost:%s -> %s:%s via %s\n' "$local_port" "$container_ip" "$remote_port" "$ssh_server"

    exec ssh -v -N -L "${local_port}:${container_ip}:${remote_port}" "$ssh_server"
    ;;

  docker-ip)
    require_arg_count "$#" 2 "$command_name"
    docker_ip "$1" "$2"
    ;;

  run)
    require_min_arg_count "$#" 2 "$command_name"
    ssh_server="$1"
    shift
    exec ssh "$ssh_server" "$@"
    ;;

  shell)
    require_arg_count "$#" 1 "$command_name"
    exec ssh "$1"
    ;;

  push)
    split_scp_args "$command_name" "$@"
    if [[ "${#scp_options[@]}" -gt 0 ]]; then
      exec scp "${scp_options[@]}" "$scp_source" "$scp_server:$scp_dest"
    fi
    exec scp "$scp_source" "$scp_server:$scp_dest"
    ;;

  pull)
    split_scp_args "$command_name" "$@"
    if [[ "${#scp_options[@]}" -gt 0 ]]; then
      exec scp "${scp_options[@]}" "$scp_server:$scp_source" "$scp_dest"
    fi
    exec scp "$scp_server:$scp_source" "$scp_dest"
    ;;

  help|-h|--help)
    usage
    ;;

  *)
    die_usage "unknown command \"$command_name\"."
    ;;
esac
