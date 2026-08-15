#!/usr/bin/env bash
#
# Run a matrix of backtests: every strategy against every timeframe, in both
# comparison and cost-sweep modes.
#
# Running one at a time is slow, and slow invites skipping the runs whose
# results look unpromising — which are exactly the runs the experiment log's
# denominator depends on. Batch running removes that excuse, and pays for it by
# making the denominator grow fast, which is why this script prints the count.
#
# Usage:
#   scripts/sweep.sh                          # everything
#   scripts/sweep.sh -s ema_crossover         # one strategy, all timeframes
#   scripts/sweep.sh -t 1h,4h                 # all strategies, two timeframes
#   scripts/sweep.sh -s ema_crossover -t 1h -m sweep
#   scripts/sweep.sh --dry-run                # print the plan, run nothing
#   scripts/sweep.sh -- --entry-order-type=limit   # extra flags for every run
#
# Options:
#   -s, --strategies   comma separated; default every registered strategy
#   -t, --timeframes   comma separated; default 1m,5m,15m,1h,4h
#   -m, --modes        cmp and/or sweep; default both, combined into one run
#   -o, --out          output directory; default results/
#       --halt-on-gaps use --allow-gaps=halt instead of skip
#       --dry-run      print what would run
#
# Everything after `--` is passed to every run, so a cost-model experiment
# needs no change here.
#
# There is deliberately no flag to suppress the experiment log. Batch running is
# exactly where the denominator inflates fastest, and hiding that would defeat
# the log's only purpose.

set -euo pipefail

cd "$(dirname "$0")/.."
readonly ROOT="${PWD}"

# The file is checked for, not sourced.
#
# The backtest binary reads .env itself by walking up from its working
# directory, and it parses the file rather than executing it — so a password or
# a connection string containing $(...), a backtick or an unquoted & arrives
# intact instead of being run by the shell. Sourcing it here would reintroduce
# exactly that, and this script runs from the repository root where the binary
# will find the same file.
if [[ ! -f "${ROOT}/.env" ]]; then
	echo "no ${ROOT}/.env — copy .env.example to .env first" >&2
	exit 1
fi

TIMEFRAMES="1m,5m,15m,1h,4h"
MODES="cmp,sweep"
OUT="results"
GAPS="skip"
DRY_RUN=0
STRATEGIES=""
PASSTHROUGH=()

while [[ $# -gt 0 ]]; do
	case $1 in
	-s | --strategies)
		STRATEGIES="$2"
		shift 2
		;;
	-t | --timeframes)
		TIMEFRAMES="$2"
		shift 2
		;;
	-m | --modes)
		MODES="$2"
		shift 2
		;;
	-o | --out)
		OUT="$2"
		shift 2
		;;
	--halt-on-gaps)
		GAPS="halt"
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--)
		shift
		PASSTHROUGH=("$@")
		break
		;;
	-h | --help)
		sed -n '2,35p' "$0"
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
done

readonly BACKTEST="${ROOT}/server/bin/backtest"

# Always rebuilt, not only when missing.
#
# A stale binary is the worst thing this script could run: it produces numbers
# that look current, appends them to the experiment log as though they were,
# and nothing in the output says which version of the engine made them. Go
# caches, so rebuilding an unchanged tree costs almost nothing.
echo "building the backtest binary"
make -C "${ROOT}" build >/dev/null

# The strategy list comes from the binary, not from this script. A strategy
# added to the registry is picked up without anyone remembering to edit here,
# which is the only way a matrix runner stays honest about covering everything.
if [[ -z ${STRATEGIES} ]]; then
	STRATEGIES="$("${BACKTEST}" --list-strategies |
		awk '/^  [a-z_]+/ { print $1 }' | paste -sd, -)"
fi
if [[ -z ${STRATEGIES} ]]; then
	echo "could not read the strategy list from --list-strategies" >&2
	exit 1
fi

IFS=',' read -r -a strategy_list <<<"${STRATEGIES}"
IFS=',' read -r -a timeframe_list <<<"${TIMEFRAMES}"
IFS=',' read -r -a mode_list <<<"${MODES}"

# One run per cell, whatever the modes.
#
# The modes used to multiply the matrix: --compare and --cost-sweep each
# execute the base run, so running them as two invocations computed the same
# configuration twice and logged it twice. The log read as 48 experiments when
# roughly 24 distinct configurations had been tested, and the count above a
# result is the only thing that says how much weight it deserves.
total=$((${#strategy_list[@]} * ${#timeframe_list[@]}))

# The flags every cell gets, built once from the requested modes.
mode_args=()
mode_label=""
for mode in "${mode_list[@]}"; do
	case ${mode} in
	cmp) mode_args+=(--compare) ;;
	sweep) mode_args+=(--cost-sweep) ;;
	*)
		echo "unknown mode: ${mode}" >&2
		exit 2
		;;
	esac
	mode_label="${mode_label:+${mode_label}-}${mode}"
done

echo
echo "strategies : ${STRATEGIES}"
echo "timeframes : ${TIMEFRAMES}"
echo "modes      : ${MODES}"
echo "gaps       : --allow-gaps=${GAPS}"
echo "output     : ${OUT}/"
if [[ ${#PASSTHROUGH[@]} -gt 0 ]]; then
	echo "extra      : ${PASSTHROUGH[*]}"
fi
echo "runs       : ${total}"

# Not a prompt. A reminder that the count is part of the result: a strategy
# picked out of N runs had N chances to look good by accident, and only the log
# records N.
if ((total > 5)); then
	echo
	echo "  This will add ${total} entries to docs/experiments.md."
	echo "  A strategy picked from ${total} runs has ${total} chances to look good by accident."
fi
echo

if ((DRY_RUN)); then
	for strategy in "${strategy_list[@]}"; do
		for timeframe in "${timeframe_list[@]}"; do
			echo "would run: ${strategy} ${timeframe} ${mode_label} -> ${OUT}/${strategy}-${timeframe}-${mode_label}.json"
		done
	done
	echo
	echo "dry run: nothing was executed and nothing was logged"
	exit 0
fi

mkdir -p "${OUT}"

completed=0
failed=()
started_all=$(date +%s)

for strategy in "${strategy_list[@]}"; do
	for timeframe in "${timeframe_list[@]}"; do
		out="${OUT}/${strategy}-${timeframe}-${mode_label}.json"

		args=(--strategy="${strategy}" --timeframe="${timeframe}"
			--allow-gaps="${GAPS}" --out="${out}")
		args+=("${mode_args[@]}")
		if [[ ${#PASSTHROUGH[@]} -gt 0 ]]; then
			args+=("${PASSTHROUGH[@]}")
		fi

		printf '  %-16s %-5s %-10s ' "${strategy}" "${timeframe}" "${mode_label}"
		started=$(date +%s)

		# Sequential, deliberately. Parallel runs would interleave their
		# appends to docs/experiments.md, and a corrupted log is worse than
		# a slow sweep.
		#
		# A failure does not stop the matrix: one strategy that cannot build
		# a filter at one timeframe should not cost the other twenty runs.
		if "${BACKTEST}" "${args[@]}" >"${out}.log" 2>&1; then
			printf 'ok    %4ss\n' "$(($(date +%s) - started))"
			completed=$((completed + 1))
		else
			printf 'FAIL  %4ss  (%s)\n' "$(($(date +%s) - started))" "${out}.log"
			failed+=("${strategy} ${timeframe} ${mode_label}")
		fi
	done
done

elapsed=$(($(date +%s) - started_all))

echo
echo "completed ${completed} of ${total} in ${elapsed}s"

if [[ ${#failed[@]} -gt 0 ]]; then
	echo
	echo "failed (${#failed[@]}):"
	for entry in "${failed[@]}"; do
		echo "  ${entry}"
	done
fi

# The number that matters afterwards. Every completed run appended an entry,
# and the count is what makes the best of them interpretable.
echo
echo "${completed} entries added to docs/experiments.md by this invocation."
echo "Count the entries above a result before acting on it."

if [[ ${#failed[@]} -gt 0 ]]; then
	exit 1
fi
