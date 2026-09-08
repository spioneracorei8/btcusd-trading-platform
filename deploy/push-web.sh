#!/usr/bin/env bash
#
# Copies the built web app to the VPS.
#
#   mobile/ $ npm run build:web
#   deploy/push-web.sh spionera.tailb78884.ts.net
#
# # Why this exists rather than a bare rsync in the runbook
#
# It used to be one, and the runbook said:
#
#     rsync -a --delete dist/ btcusd@<host>:/srv/btcusd/web/
#
# `--delete` removes everything at the destination that is not in the source.
# Pointed at the wrong path — and `<host>` is not the only thing in that line
# somebody has to fill in — it deletes whatever is there. It was pointed at
# /opt/btcusd once, which is the checkout, and took the working tree, the .env
# and the git directory with it.
#
# The deletion is needed: the service worker precaches fingerprinted filenames
# from the build it was stamped with, so a directory still holding the previous
# build's bundle is one where the old one answers 200. What was missing is any
# check that the destination is a place an export belongs.
set -euo pipefail

HOST=${1:-}
USER_AT=${DEPLOY_USER:-btcusd}
DEST=${WEB_ROOT_HOST:-/srv/btcusd/web}
SOURCE=${SOURCE:-dist}

if [ -z "${HOST}" ]; then
	echo "usage: deploy/push-web.sh <host>            # e.g. spionera.tailb78884.ts.net" >&2
	echo "       WEB_ROOT_HOST=/srv/btcusd/web  DEPLOY_USER=btcusd  SOURCE=dist" >&2
	exit 1
fi

# The source has to be an export, or there is nothing worth sending and
# --delete would empty the destination.
for required in index.html sw.js manifest.json; do
	if [ ! -f "${SOURCE}/${required}" ]; then
		echo "${SOURCE} has no ${required}; run 'npm run build:web' in mobile/ first" >&2
		exit 1
	fi
done

TARGET="${USER_AT}@${HOST}:${DEST}"

# The destination has to be empty, absent, or a previous export. Anything else
# is somebody's directory and --delete would take it.
#
# Checked on the far side, because that is the only place that knows.
echo "checking ${TARGET}"
ssh "${USER_AT}@${HOST}" "DEST='${DEST}' bash -s" <<'REMOTE'
set -euo pipefail
if [ ! -e "${DEST}" ]; then
	echo "  ${DEST} does not exist yet; it will be created"
	exit 0
fi
if [ ! -d "${DEST}" ]; then
	echo "  ${DEST} exists and is not a directory" >&2
	exit 1
fi
if [ -z "$(ls -A "${DEST}")" ]; then
	echo "  ${DEST} is empty"
	exit 0
fi
if [ -f "${DEST}/index.html" ] && [ -f "${DEST}/sw.js" ]; then
	echo "  ${DEST} holds a previous export"
	exit 0
fi
echo "  ${DEST} holds something that is not a web export:" >&2
ls -A "${DEST}" | head -10 >&2
echo "" >&2
echo "  Refusing: --delete would remove it. Check WEB_ROOT_HOST." >&2
exit 1
REMOTE

echo "syncing ${SOURCE}/ to ${TARGET}"
rsync -a --delete "${SOURCE}/" "${TARGET}/"
echo "done"
