#!/usr/bin/env bash
#
# PostgreSQL dump with rotation, run daily by btcusd-backup.timer.
#
# `data_gaps` exists because the system records what it could not fetch. A lost
# database is the same problem several orders of magnitude larger, except
# nothing records it and nothing can backfill it — Binance serves recent klines,
# not two years of them at 1m on demand.
#
# The guard rails matter more than the dump. A backup script that silently
# writes empty files is worse than no backup at all, because it removes the
# worry that would otherwise prompt somebody to check.
#
# Usage:
#   ./backup.sh                 # dump, verify, rotate
#   BACKUP_MODE=host ./backup.sh    # use host pg_dump against DATABASE_URL
#                                   # instead of the compose container
#
# Environment:
#   APP_DIR           repository checkout   (default /opt/btcusd)
#   BACKUP_DIR        where dumps live     (default /var/backups/btcusd)
#   BACKUP_MIN_BYTES  absolute floor       (default 10240)
#   BACKUP_SHRINK_PCT fail if the dump is smaller than this percentage of the
#                     previous one        (default 50)
#   KEEP_DAILY        (default 7)
#   KEEP_WEEKLY       (default 4)

set -euo pipefail

readonly APP_DIR="${APP_DIR:-/opt/btcusd}"
readonly BACKUP_DIR="${BACKUP_DIR:-/var/backups/btcusd}"
readonly BACKUP_MODE="${BACKUP_MODE:-compose}"
readonly BACKUP_MIN_BYTES="${BACKUP_MIN_BYTES:-10240}"
readonly BACKUP_SHRINK_PCT="${BACKUP_SHRINK_PCT:-50}"
readonly KEEP_DAILY="${KEEP_DAILY:-7}"
readonly KEEP_WEEKLY="${KEEP_WEEKLY:-4}"

readonly DAILY_DIR="${BACKUP_DIR}/daily"
readonly WEEKLY_DIR="${BACKUP_DIR}/weekly"

# UTC everywhere, like the rest of the system. A backup timestamped in local
# time cannot be lined up against the logs of the incident that prompted it.
stamp() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }
log()   { printf '%s [backup] %s\n' "$(stamp)" "$*"; }
warn()  { printf '%s [backup] WARN  %s\n' "$(stamp)" "$*" >&2; }
die()   { printf '%s [backup] ERROR %s\n' "$(stamp)" "$*" >&2; exit 1; }

human() {
	local bytes=$1
	if command -v numfmt >/dev/null 2>&1; then
		numfmt --to=iec --suffix=B "${bytes}"
	else
		printf '%s bytes' "${bytes}"
	fi
}

# load_env reads the deployment .env without executing it.
#
# `source`ing it would run whatever a stray backtick in a password produced,
# and the file is written by a generator on a host nobody logs into.
load_env() {
	local file="${APP_DIR}/.env"
	[[ -f ${file} ]] || return 0

	local line key value
	while IFS= read -r line || [[ -n ${line} ]]; do
		[[ ${line} =~ ^[[:space:]]*# ]] && continue
		[[ ${line} =~ ^[[:space:]]*$ ]] && continue
		[[ ${line} != *=* ]] && continue
		key="${line%%=*}"
		value="${line#*=}"
		key="${key//[[:space:]]/}"
		# Only take what this script needs, so an unrelated variable in .env
		# cannot shadow something in the environment here.
		case ${key} in
		POSTGRES_USER | POSTGRES_DB | DATABASE_URL)
			# Strip one layer of surrounding quotes if present.
			value="${value%\"}"
			value="${value#\"}"
			export "${key}=${value}"
			;;
		esac
	done <"${file}"
}

# dump_to writes a compressed dump to $1, or fails.
dump_to() {
	local target=$1

	case ${BACKUP_MODE} in
	compose)
		# Running pg_dump inside the container guarantees the client version
		# matches the server. A host client one major version behind refuses
		# to dump at all, which would surface as a broken backup rather than
		# as an obvious installation problem.
		#
		# `set -o pipefail` is what makes a pg_dump failure here fatal rather
		# than being masked by gzip exiting 0 on empty input.
		docker compose \
			--env-file "${APP_DIR}/.env" \
			-f "${APP_DIR}/deploy/docker-compose.yml" \
			-f "${APP_DIR}/deploy/docker-compose.prod.yml" \
			exec -T postgres \
			pg_dump -U "${POSTGRES_USER:-trading}" -d "${POSTGRES_DB:-btcusd}" |
			gzip -9 >"${target}"
		;;
	host)
		[[ -n ${DATABASE_URL:-} ]] || die "BACKUP_MODE=host needs DATABASE_URL"
		pg_dump "${DATABASE_URL}" | gzip -9 >"${target}"
		;;
	*)
		die "BACKUP_MODE must be 'compose' or 'host', got '${BACKUP_MODE}'"
		;;
	esac
}

# rotate keeps the newest $2 files in directory $1 and deletes the rest.
#
# Names are ISO timestamps, so a lexicographic sort is a chronological one.
rotate() {
	local dir=$1 keep=$2 removed=0

	# `|| true` on the pipeline: tail closing the pipe early makes ls exit
	# non-zero under pipefail, which is not an error here.
	local stale
	stale="$( (ls -1 "${dir}"/btcusd-*.sql.gz 2>/dev/null || true) | sort -r | tail -n +"$((keep + 1))")"

	[[ -z ${stale} ]] && return 0
	while IFS= read -r file; do
		[[ -n ${file} ]] || continue
		rm -f -- "${file}"
		removed=$((removed + 1))
	done <<<"${stale}"
	log "rotated ${dir##*/}: removed ${removed}, keeping ${keep}"
}

# previous_size returns the byte size of the most recent daily dump other than
# $1, or 0 when there is none.
previous_size() {
	local exclude=$1 newest
	newest="$( (ls -1 "${DAILY_DIR}"/btcusd-*.sql.gz 2>/dev/null || true) |
		grep -vF -- "${exclude}" | sort -r | head -1)"
	[[ -n ${newest} ]] || { printf '0'; return 0; }
	stat -c %s "${newest}"
}

main() {
	load_env

	install -d -m 700 "${DAILY_DIR}" "${WEEKLY_DIR}"

	local name="btcusd-$(date -u '+%Y%m%dT%H%M%SZ').sql.gz"
	local target="${DAILY_DIR}/${name}"

	log "starting dump (mode=${BACKUP_MODE}) -> ${target}"
	if ! dump_to "${target}"; then
		rm -f -- "${target}"
		die "pg_dump failed; the partial file was removed"
	fi

	# --- the guards ---------------------------------------------------------

	local size
	size="$(stat -c %s "${target}")"
	log "dump size: $(human "${size}")"

	if ((size < BACKUP_MIN_BYTES)); then
		rm -f -- "${target}"
		die "dump is $(human "${size}"), below the floor of $(human "${BACKUP_MIN_BYTES}"). A dump this small is not a backup of this database. The file was removed so it cannot be mistaken for one."
	fi

	# gzip -t reads the whole stream and checks the CRC, so a dump truncated by
	# a full disk or a killed container fails here rather than at 3am during a
	# restore that was supposed to be the recovery.
	if ! gzip -t "${target}" 2>/dev/null; then
		rm -f -- "${target}"
		die "the dump is not a valid gzip stream; it was truncated. The file was removed."
	fi

	# Shrinking against the last dump catches what an absolute floor cannot:
	# a database that still dumps cleanly but has lost most of its rows.
	local previous
	previous="$(previous_size "${target}")"
	if ((previous > 0)); then
		local ratio=$((size * 100 / previous))
		log "previous dump: $(human "${previous}") — this one is ${ratio}% of it"
		if ((ratio < BACKUP_SHRINK_PCT)); then
			# Not deleted. A shrunken dump is still evidence, and it may be
			# the only copy if the database really did lose data.
			warn "this dump is ${ratio}% of the previous one, below the ${BACKUP_SHRINK_PCT}% threshold."
			warn "KEPT at ${target} — check whether the database lost data before trusting either file."
			die "backup shrank unexpectedly; investigate before the next rotation removes the older copies"
		fi
	else
		log "no previous dump to compare against; this is the first"
	fi

	# --- weekly copy and rotation ------------------------------------------

	# ISO weekday 7 is Sunday. A hard link costs no extra space, and the two
	# directories then rotate independently: the weekly copy survives the daily
	# window closing over it.
	if [[ $(date -u '+%u') == 7 ]]; then
		ln -f -- "${target}" "${WEEKLY_DIR}/${name}"
		log "linked into weekly/"
	fi

	rotate "${DAILY_DIR}" "${KEEP_DAILY}"
	rotate "${WEEKLY_DIR}" "${KEEP_WEEKLY}"

	log "backup complete: ${target} ($(human "${size}"))"
}

main "$@"
