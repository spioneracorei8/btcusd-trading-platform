#!/usr/bin/env bash
#
# Restore a dump into a scratch database and check it against the live one.
#
# An untested backup is a hypothesis. This turns it into a fact, and it is
# meant to be run once during setup and again whenever anything about the
# database changes — a major version bump, a new extension, a schema migration
# large enough to be worth worrying about.
#
# It never touches the live database. The restore goes into a scratch database
# that is dropped afterwards.
#
# Usage:
#   ./restore-test.sh                  # newest daily dump
#   ./restore-test.sh /path/dump.sql.gz
#   KEEP_SCRATCH=1 ./restore-test.sh   # leave the scratch database behind
#
# Environment: as backup.sh, plus
#   SCRATCH_DB   name of the throwaway database (default btcusd_restore_test)

set -euo pipefail

readonly APP_DIR="${APP_DIR:-/opt/btcusd}"
readonly BACKUP_DIR="${BACKUP_DIR:-/var/backups/btcusd}"
readonly BACKUP_MODE="${BACKUP_MODE:-compose}"
readonly SCRATCH_DB="${SCRATCH_DB:-btcusd_restore_test}"
readonly KEEP_SCRATCH="${KEEP_SCRATCH:-0}"

# The tables whose row counts have to match. collector_status is deliberately
# included: it is small, but a restore that silently lost it would leave the
# api unable to answer /internal/market/status after a recovery.
readonly TABLES=(candles signals data_gaps collector_status)

stamp() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }
log()   { printf '%s [restore-test] %s\n' "$(stamp)" "$*"; }
die()   { printf '%s [restore-test] ERROR %s\n' "$(stamp)" "$*" >&2; exit 1; }

load_env() {
	local file="${APP_DIR}/.env" line key value
	[[ -f ${file} ]] || return 0
	while IFS= read -r line || [[ -n ${line} ]]; do
		[[ ${line} =~ ^[[:space:]]*# ]] && continue
		[[ ${line} != *=* ]] && continue
		key="${line%%=*}"; key="${key//[[:space:]]/}"
		value="${line#*=}"; value="${value%\"}"; value="${value#\"}"
		case ${key} in
		POSTGRES_USER | POSTGRES_DB | DATABASE_URL) export "${key}=${value}" ;;
		esac
	done <"${file}"
}

# psql_db runs SQL against database $1, reading the statement from stdin.
psql_db() {
	local db=$1
	case ${BACKUP_MODE} in
	compose)
		docker compose \
			--env-file "${APP_DIR}/.env" \
			-f "${APP_DIR}/deploy/docker-compose.yml" \
			-f "${APP_DIR}/deploy/docker-compose.prod.yml" \
			exec -T postgres psql -v ON_ERROR_STOP=1 -qtAX \
			-U "${POSTGRES_USER:-trading}" -d "${db}"
		;;
	host)
		[[ -n ${DATABASE_URL:-} ]] || die "BACKUP_MODE=host needs DATABASE_URL"
		# Swap the database out of the URL, keeping host, port and credentials.
		local url="${DATABASE_URL}"
		local prefix="${url%%\?*}" query=""
		[[ ${url} == *\?* ]] && query="?${url#*\?}"
		psql -v ON_ERROR_STOP=1 -qtAX "${prefix%/*}/${db}${query}"
		;;
	*) die "BACKUP_MODE must be 'compose' or 'host'" ;;
	esac
}

count_rows() {
	local db=$1 table=$2 exists out

	# A table that does not exist is a different failure from a table that
	# exists and is empty. Conflating the two is how a restore that dropped a
	# table passes its own check.
	#
	# Existence is asked separately rather than with a CASE around the count:
	# PostgreSQL parses both branches of a CASE before evaluating either, so a
	# missing table raises "relation does not exist" at parse time and the
	# branch that was supposed to report it cleanly never runs.
	exists="$(printf "SELECT to_regclass('public.%s') IS NOT NULL;" "${table}" | psql_db "${db}" 2>/dev/null || true)"
	if [[ ${exists//[[:space:]]/} != "t" ]]; then
		printf 'ABSENT'
		return 0
	fi

	out="$(printf 'SELECT count(*) FROM %s;' "${table}" | psql_db "${db}")"
	printf '%s' "${out//[[:space:]]/}"
}

main() {
	load_env

	local dump="${1:-}"
	if [[ -z ${dump} ]]; then
		dump="$( (ls -1 "${BACKUP_DIR}"/daily/btcusd-*.sql.gz 2>/dev/null || true) | sort -r | head -1)"
		[[ -n ${dump} ]] || die "no dump found in ${BACKUP_DIR}/daily"
	fi
	[[ -f ${dump} ]] || die "${dump} does not exist"

	log "testing ${dump}"
	gzip -t "${dump}" 2>/dev/null || die "${dump} is not a valid gzip stream"

	local live_db="${POSTGRES_DB:-btcusd}"

	log "counting rows in the live database (${live_db})"
	local -a live_counts=()
	local table
	for table in "${TABLES[@]}"; do
		live_counts+=("$(count_rows "${live_db}" "${table}")")
	done

	log "creating the scratch database ${SCRATCH_DB}"
	printf 'DROP DATABASE IF EXISTS %s;' "${SCRATCH_DB}" | psql_db postgres >/dev/null
	printf 'CREATE DATABASE %s;' "${SCRATCH_DB}" | psql_db postgres >/dev/null

	cleanup() {
		if [[ ${KEEP_SCRATCH} == 1 ]]; then
			log "leaving ${SCRATCH_DB} in place (KEEP_SCRATCH=1)"
			return
		fi
		printf 'DROP DATABASE IF EXISTS %s;' "${SCRATCH_DB}" | psql_db postgres >/dev/null 2>&1 || true
	}
	trap cleanup EXIT

	# TimescaleDB needs this. Restoring a dump containing hypertables without
	# it leaves the catalog inconsistent with the chunks — the restore appears
	# to succeed and the hypertable is subtly broken, which is exactly the
	# failure a restore test exists to catch. The functions only exist once the
	# extension is created, so this is conditional rather than assumed.
	local has_timescale
	has_timescale="$(printf "SELECT count(*) FROM pg_available_extensions WHERE name = 'timescaledb';" |
		psql_db "${SCRATCH_DB}")"

	if [[ ${has_timescale//[[:space:]]/} != 0 ]]; then
		log "timescaledb present — running timescaledb_pre_restore()"
		printf "CREATE EXTENSION IF NOT EXISTS timescaledb; SELECT timescaledb_pre_restore();" |
			psql_db "${SCRATCH_DB}" >/dev/null
	else
		log "timescaledb not available here — restoring without the pre/post hooks"
	fi

	log "restoring"
	# ON_ERROR_STOP is set in psql_db, so a failed statement aborts rather than
	# producing a half-restored database that still passes a shallow check.
	if ! gunzip -c "${dump}" | psql_db "${SCRATCH_DB}" >/dev/null; then
		die "the restore failed — this backup would not have recovered the system"
	fi

	if [[ ${has_timescale//[[:space:]]/} != 0 ]]; then
		log "running timescaledb_post_restore()"
		printf "SELECT timescaledb_post_restore();" | psql_db "${SCRATCH_DB}" >/dev/null
	fi

	log "comparing row counts"
	local failed=0 index=0 live restored
	for table in "${TABLES[@]}"; do
		live="${live_counts[${index}]}"
		restored="$(count_rows "${SCRATCH_DB}" "${table}")"
		index=$((index + 1))

		if [[ ${live} == "${restored}" ]]; then
			printf '  %-18s %12s  ok\n' "${table}" "${restored}"
		else
			printf '  %-18s %12s  MISMATCH (live has %s)\n' "${table}" "${restored}" "${live}"
			failed=1
		fi
	done

	# A dump taken while the collector is running will legitimately be a few
	# candles behind by the time this comparison runs, so an exact match on
	# `candles` is not guaranteed on a live host. The runbook says to read a
	# small positive difference as normal and anything else as a problem.
	if ((failed)); then
		die "row counts differ. If the collector is running, a few candles of drift is expected; a large or negative difference is not."
	fi

	log "restore test passed — the dump reconstructs the database"
}

main "$@"
