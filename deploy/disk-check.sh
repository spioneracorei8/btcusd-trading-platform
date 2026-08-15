#!/usr/bin/env bash
#
# Disk usage alarm, run hourly by btcusd-disk-check.timer.
#
# If the disk fills, PostgreSQL stops accepting writes and the collector starts
# failing in a way that takes a while to recognise as a disk problem: the logs
# fill with write errors that look like database errors. 60 GB is comfortable
# now and will not be forever — a year of 1m candles across four timeframes is
# not large, but backups, WAL and container logs accumulate beside it.
#
# Output goes to journald with sd-daemon severity prefixes, so `journalctl -u
# btcusd-disk-check -p warning` shows only the entries that mattered.
#
# Usage:
#   ./disk-check.sh
#
# Environment:
#   WARN_PCT      warn at or above this  (default 75)
#   CRIT_PCT      critical at or above   (default 90)
#   BACKUP_DIR    (default /var/backups/btcusd)
#   PGDATA_PATH   override the auto-detected PostgreSQL volume mountpoint

set -euo pipefail

readonly WARN_PCT="${WARN_PCT:-75}"
readonly CRIT_PCT="${CRIT_PCT:-90}"
readonly BACKUP_DIR="${BACKUP_DIR:-/var/backups/btcusd}"
readonly VOLUME_NAME="${VOLUME_NAME:-btcusd-trading-platform_pgdata}"

# sd-daemon severity prefixes. systemd strips them and files the line at that
# priority; without them every line is "info" and a critical alert reads the
# same as a routine one.
readonly ERR='<3>'
readonly WARNING='<4>'
readonly INFO='<6>'

exit_code=0

# pct_of prints the used percentage of the filesystem containing $1.
pct_of() {
	df -P "$1" 2>/dev/null | awk 'NR==2 {gsub(/%/,"",$5); print $5}'
}

# human_df prints "used of size (avail free)" for the filesystem containing $1.
human_df() {
	df -Ph "$1" 2>/dev/null | awk 'NR==2 {printf "%s of %s used, %s free", $3, $2, $4}'
}

# report checks one path and emits a line at the right severity.
report() {
	local label=$1 path=$2

	if [[ ! -e ${path} ]]; then
		printf '%s%s: %s does not exist\n' "${INFO}" "${label}" "${path}"
		return 0
	fi

	local pct
	pct="$(pct_of "${path}")"
	if [[ -z ${pct} ]]; then
		printf '%s%s: could not read usage for %s\n' "${WARNING}" "${label}" "${path}"
		return 0
	fi

	local detail
	detail="$(human_df "${path}")"

	if ((pct >= CRIT_PCT)); then
		printf '%s%s CRITICAL: %s%% used (%s) — PostgreSQL stops accepting writes when this fills\n' \
			"${ERR}" "${label}" "${pct}" "${detail}"
		exit_code=1
	elif ((pct >= WARN_PCT)); then
		printf '%s%s WARNING: %s%% used (%s)\n' "${WARNING}" "${label}" "${pct}" "${detail}"
	else
		printf '%s%s ok: %s%% used (%s)\n' "${INFO}" "${label}" "${pct}" "${detail}"
	fi
}

# pgdata_path resolves where the PostgreSQL named volume actually lives.
#
# Checking only the root filesystem would miss the case the spec cares about:
# a separate disk mounted for the volume filling while / stays comfortable.
pgdata_path() {
	if [[ -n ${PGDATA_PATH:-} ]]; then
		printf '%s' "${PGDATA_PATH}"
		return
	fi
	if command -v docker >/dev/null 2>&1; then
		local mount
		mount="$(docker volume inspect -f '{{.Mountpoint}}' "${VOLUME_NAME}" 2>/dev/null || true)"
		if [[ -n ${mount} ]]; then
			printf '%s' "${mount}"
			return
		fi
	fi
	printf ''
}

main() {
	report "root filesystem" "/"

	local pgdata
	pgdata="$(pgdata_path)"
	if [[ -n ${pgdata} ]]; then
		report "postgres volume" "${pgdata}"
		# The directory's own size, which is the number that grows and the one
		# to compare week over week when working out how fast candles
		# accumulate. -x stops it wandering onto another filesystem.
		local used
		used="$(du -shx "${pgdata}" 2>/dev/null | cut -f1 || true)"
		[[ -n ${used} ]] && printf '%spostgres volume size: %s (%s)\n' "${INFO}" "${used}" "${pgdata}"
	else
		printf '%spostgres volume %s not found — is the stack up?\n' "${WARNING}" "${VOLUME_NAME}"
	fi

	report "backups" "${BACKUP_DIR}"
	if [[ -d ${BACKUP_DIR} ]]; then
		local backup_size
		backup_size="$(du -shx "${BACKUP_DIR}" 2>/dev/null | cut -f1 || true)"
		[[ -n ${backup_size} ]] && printf '%sbackup directory size: %s\n' "${INFO}" "${backup_size}"
	fi

	exit "${exit_code}"
}

main "$@"
