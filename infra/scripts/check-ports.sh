#!/bin/sh
# Usage: check-ports.sh PORT...
# Fails (exit 1) when a development port is held by anything other than the Tally dev compose project,
# and names the owner. Never picks another port: stop the owner (or run that project with its own
# `docker compose -p`) instead. Ports already published by the Tally dev project are reused, not duplicated.
set -u

project="${TALLY_DEV_PROJECT:-tally}"

if [ "$#" -eq 0 ]; then
	echo "usage: $0 PORT..." >&2
	exit 2
fi

has() { command -v "$1" >/dev/null 2>&1; }

listening() {
	if has ss; then
		[ -n "$(ss -Hltn "sport = :$1" 2>/dev/null)" ]
	elif has lsof; then
		lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
	else
		echo "check-ports: neither ss nor lsof is installed" >&2
		exit 2
	fi
}

# Container (if any) of the Tally dev project that publishes the port.
owned_by_project() {
	has docker || return 1
	[ -n "$(docker ps -q --filter "publish=$1" --filter "label=com.docker.compose.project=$project" 2>/dev/null)" ]
}

describe_owner() {
	if has docker; then
		docker ps --filter "publish=$1" \
			--format '  container {{.Names}} (project {{.Label "com.docker.compose.project"}}): {{.Ports}}' 2>/dev/null
	fi
	if has lsof; then
		lsof -nP -iTCP:"$1" -sTCP:LISTEN 2>/dev/null | sed 's/^/  /'
	fi
	if has ss; then
		ss -Hltnp "sport = :$1" 2>/dev/null | sed 's/^/  /'
	fi
}

busy=0
for port in "$@"; do
	case "$port" in
	'' | *[!0-9]*)
		echo "check-ports: invalid port '$port'" >&2
		exit 2
		;;
	esac
	if listening "$port" && ! owned_by_project "$port"; then
		echo "Port $port is already in use by:"
		describe_owner "$port"
		busy=1
	fi
done

if [ "$busy" -ne 0 ]; then
	echo "Stop the process above (or move that project with 'docker compose -p'); Tally never switches ports."
	echo "Only the Postgres host port is configurable: POSTGRES_HOST_PORT (default 5442)."
	exit 1
fi
