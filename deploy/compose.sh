#!/usr/bin/env bash
#
# Runs docker compose with the overlays this deployment needs.
#
# The overlay list is not fixed: the web app is mounted only when there is one
# to mount, because a bind mount whose source is absent makes Docker create a
# directory in its place, and WEB_ROOT pointing at an empty directory is a
# deployment that answers every page with 404 and looks like a build problem.
#
# It exists so that btcusd.service's ExecStart and ExecStop cannot drift apart:
# a `down` computed from a different overlay list than the `up` leaves
# containers behind.
#
#   deploy/compose.sh up -d --remove-orphans
#   deploy/compose.sh down
#
# There is no notify overlay any more. It existed to mount an FCM service
# account key; Web Push needs two environment variables and no file, so the
# overlay went with the transport (phase 09b part C).
set -euo pipefail

APP_DIR=${APP_DIR:-/opt/btcusd}
ENV_FILE="${APP_DIR}/.env"

if [ ! -f "${ENV_FILE}" ]; then
	echo "${ENV_FILE} does not exist; nothing can start without it" >&2
	exit 1
fi

files=(
	-f "${APP_DIR}/deploy/docker-compose.yml"
	-f "${APP_DIR}/deploy/docker-compose.prod.yml"
)

# A set WEB_ROOT_HOST means "serve the app as well as the API". An empty or
# commented-out value means the api serves the API alone, which is a valid
# deployment and not an error.
if grep -qE '^[[:space:]]*WEB_ROOT_HOST=[^[:space:]#]' "${ENV_FILE}"; then
	files+=(-f "${APP_DIR}/deploy/docker-compose.web.yml")
fi

exec docker compose --env-file "${ENV_FILE}" "${files[@]}" "$@"
