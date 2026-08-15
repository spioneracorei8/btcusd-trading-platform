# 0017 — Running the collector on a VPS

**Status:** accepted · **Date:** 2026-08-15 · **Phase:** deployment

## Context

The collector needs to run 24/7 so the candle series keeps accumulating.
Everything before this ran on a developer machine that gets closed at night,
which is fine for backtesting and useless for collecting.

Target: 2 vCPU / 4 GB / 60 GB, Ubuntu 24.04, one public IPv4.

This is **not** a trading launch. No strategy runs on this host, and nothing
places orders. The machine has one job — stay up and keep the series complete —
and every decision below is measured against that.

## Decisions

### 1. Docker on the VPS, though development uses podman

The runbook has to state one command that works. Docker's apt repository ships
a current engine and the compose v2 plugin together, and `restart:
unless-stopped` is honoured by a daemon that is already running at boot.

Under rootless podman the same policy needs `podman-restart.service` enabled
*and* lingering enabled for the user. Miss either and the stack silently does
not come back after a reboot — the one failure this host cannot tolerate, and
one that is invisible until the next reboot happens to be unplanned.

The development machine is unaffected: `make engine` still auto-detects, and
the compose files are engine-neutral. The cost is that the two environments
differ, which is a real cost; it is smaller than a collector that quietly stops
existing after a power event.

### 2. The API is published on loopback and the tailnet, and the compose file
refuses to start if it does not know the tailnet address

`ports:` gets `${TAILSCALE_IP:?...}`. An unset variable aborts the command with
a message naming the file to edit.

The alternative — a default — is what makes this dangerous. An empty value
inside a port mapping expands to `":8080:8080"`, which binds every interface
including the public one. `docker compose ps` renders that identically to the
safe case, so the mistake would not be visible in the place someone would look.
Refusing to start is the only behaviour that cannot fail silently.

### 3. PostgreSQL stays on loopback, not on the tailnet

The deployment spec grouped PostgreSQL with the API as "reached over
Tailscale". Nothing actually needs it: the mobile app and the phase 08 API both
speak HTTP, and the only SQL consumer is a human with a `psql` prompt.

So the database is published on `127.0.0.1` only and reached through an SSH
tunnel, documented in the runbook. This is a deliberate deviation, recorded
here rather than made quietly — it costs one extra flag on the rare occasion
somebody wants a shell, and removes a listening database from the tailnet for
good.

### 4. systemd orchestrates compose, and waits for the tailnet address

`btcusd.service` is `Type=oneshot` with `RemainAfterExit=yes`. The containers'
own `restart: unless-stopped` handles a single service dying; the unit exists
for the whole host coming back.

Its `ExecStartPre` waits up to 60 seconds for `tailscale0` to have an IPv4
address. `After=tailscaled.service` is not sufficient: the daemon being started
is not the same as the interface having an address, and Docker publishing to an
address that does not exist yet fails with "cannot assign requested address".
The stack then stays down until a human notices — hours of missing candles from
a reboot at 3am.

### 5. The backup script's guards matter more than the backup

A backup script that silently writes empty files is worse than no backup,
because it removes the worry that would otherwise prompt a check. Three guards,
each catching something the others do not:

- **An absolute floor** catches an empty or failed dump. The file is deleted so
  it cannot be mistaken for a backup.
- **`gzip -t`** catches truncation from a full disk or a killed container. It
  reads the whole stream, so it costs real time and is worth it: the
  alternative is discovering the truncation during the restore that was
  supposed to be the recovery.
- **A shrink ratio against the previous dump** catches what neither of the
  above can — a database that still dumps cleanly but has lost rows.

The shrunken dump is **kept**, unlike the other two failures. If the data
really is gone, that file may be the only copy of what is left, and deleting
evidence to keep a directory tidy is the wrong trade.

### 6. Restoring a TimescaleDB dump needs the pre/post hooks

`timescaledb_pre_restore()` and `timescaledb_post_restore()` bracket the
restore. Without them the catalog and the chunks disagree: the restore appears
to succeed and the hypertable is subtly broken.

This is exactly the class of failure a restore test exists to catch, which is
why `restore-test.sh` exists at all and why the runbook says to run it once
during setup and again after any database-level change. An untested backup is a
hypothesis.

### 7. SSH hardening is opt-in, guarded, and never part of `all`

`setup.sh all` deliberately excludes `harden-ssh`. Locking yourself out of a
fresh VPS is a rebuild, and it is the single most likely way this deployment
goes badly wrong.

The step refuses to run when the deploy user has no `authorized_keys`, stops
and requires a typed confirmation that a second session works, writes a
drop-in rather than editing `sshd_config` (so a re-run overwrites one small
known file instead of accumulating duplicate directives), and validates with
`sshd -t` before reloading — removing the drop-in again if validation fails.

### 8. Container logs are capped

10 MB × 3 per service. An unbounded json-file log on a 60 GB disk is a
slow-motion outage that ends with PostgreSQL refusing writes, which presents as
a database problem rather than a disk one. The disk check exists for the same
reason and reports the PostgreSQL volume specifically, not just `/`.

## Known limitation: the backups are on the same disk

`/var/backups/btcusd` lives on the same 60 GB SSD as the database. It protects
against a dropped table, a bad migration, or a corrupted database. It does
**not** protect against losing the VPS, the disk, or the provider account —
in any of those the database and every backup go together.

This is not solved here and should not be treated as solved. The honest
statement is that this deployment has local backups only. Copying a weekly dump
off the host is the obvious next step and is deliberately out of scope for now;
it is written down so it is a known gap rather than a discovered one.

## Consequences

- The volume `btcusd-trading-platform_pgdata` is the thing that must survive.
  The runbook names it in its facts table, because `docker compose down -v` is
  one character away from `down` and two years of 1m candles cannot be
  re-fetched from Binance.
- Deployment is `git pull && systemctl restart btcusd`. Migrations apply
  automatically via the `migrate` service; there is no manual step and there
  must not be one.
- The 48-hour test is the point of having a real host. Binance closes
  WebSocket connections roughly daily, and the phase 02 fix for dropped
  confirmed candles on disconnect has only ever been verified against fakes.
  Until that test passes, the fix is untested against the real exchange.
- None of this was executed. The scripts and units were checked as far as a
  sandbox allows — `backup.sh` and `restore-test.sh` against a real PostgreSQL,
  the compose overlay through `docker compose config`, the units through
  `systemd-analyze verify` — and the entire §6 checklist and §7 test remain to
  be run on the actual machine.
