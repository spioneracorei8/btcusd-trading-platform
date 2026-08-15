# Deployment — First VPS Setup

> Read `CLAUDE.md` fully before starting.
> Target: VisperHost Cloud VPS 02 (2 vCPU / 4 GB RAM / 60 GB SSD), Ubuntu 24.04, single public IPv4.
> Purpose: run the **collector** continuously so market data keeps accumulating. Backtesting stays on the developer machine.
> **This is not a trading launch.** No strategy runs here yet, and nothing places orders.

The machine has one job: stay up and keep the candle series complete. Everything in this document serves that.

---

## 1. Deliverables

- `deploy/README.md` — the runbook, written so it can be followed months from now without reconstructing anything from memory
- `deploy/setup.sh` — idempotent provisioning script, safe to re-run
- `deploy/backup.sh` — Postgres dump + rotation
- `deploy/disk-check.sh` — disk usage alarm
- systemd units where appropriate
- Any changes to `docker-compose.yml` needed for a production profile

Scripts must be idempotent. A half-finished run followed by a re-run must converge, not compound.

---

## 2. Base hardening

Do these before anything else listens on a port.

- Create a non-root user with sudo. All subsequent work happens as that user.
- SSH: key-only. Disable `PasswordAuthentication` and `PermitRootLogin`. **Verify the key works in a second session before closing the first** — locking yourself out of a fresh VPS is a rebuild, and the runbook must say this explicitly.
- `ufw`: default deny incoming, allow outgoing. Allow SSH only. Do **not** open 5432 or 8080 to the internet — Postgres and the API are reached over Tailscale.
- `unattended-upgrades` for security patches.
- Set timezone to UTC. Not Asia/Bangkok. The entire system stores UTC (`CLAUDE.md` §4) and a mismatched host timezone makes log correlation needlessly confusing.
- Add swap: 2 GB file. With 4 GB RAM and Postgres plus two Go services, swap is cheap insurance against an OOM kill taking down the collector. It is not a substitute for adequate RAM.

---

## 3. Container runtime

The developer machine runs podman; this host should run whatever the runbook can state unambiguously. Pick one, install it, and record which in `deploy/README.md`.

If Docker: install from Docker's official apt repository, not the Ubuntu package. Add the user to the `docker` group.

The `make engine` target added earlier must report correctly on this host too.

---

## 4. Application deployment

```
/opt/btcusd/          # repo checkout
/opt/btcusd/.env      # secrets, mode 600, never committed
```

- Clone over HTTPS with a read-only deploy token, or SSH with a deploy key. The VPS never pushes.
- `.env` is created on the host from `.env.example`. Generate a strong Postgres password here — do not reuse the development one.
- Postgres data lives in a named volume. Document the volume name in the runbook; it is the thing that must survive everything else.

**Production compose profile:**

- `postgres`, `migrate`, `collector` — these three run
- `api` runs, bound to `127.0.0.1` and the Tailscale interface only, never `0.0.0.0`
- No source mounts, no exposed ports beyond the above
- `restart: unless-stopped` on postgres, api, collector
- Postgres tuning for 4 GB: set `shared_buffers` to about 1 GB and `effective_cache_size` to about 2 GB. Defaults assume a far smaller machine and will make Phase 04 backtests slower than they need to be if you ever run one here.

Bring the stack up with systemd so it survives reboots: a unit that runs compose up on boot and down on stop.

---

## 5. Tailscale

- Install, join the tailnet, enable as a service.
- Bind the api service to the Tailscale IP.
- The runbook records the machine's tailnet name — Phase 08 and the mobile app will need it.
- Do not enable Tailscale SSH in place of key-based SSH. Keep a path in that does not depend on a third party being reachable.

---

## 6. Backups

`data_gaps` records what the system could not fetch. A lost database is a far larger version of the same problem, except nothing records it.

`deploy/backup.sh`:

- `pg_dump` compressed, to `/var/backups/btcusd/`
- Daily via systemd timer
- Keep 7 daily and 4 weekly, delete older
- Log success and failure with timestamps
- **Print the dump size and fail loudly if it is implausibly small.** A backup script that silently writes empty files is worse than none, because it removes the worry that would otherwise prompt a check.

Add a restore procedure to the runbook and **perform it once** during setup — restore into a scratch database and confirm the row count matches. An untested backup is a hypothesis.

---

## 7. Disk monitoring

If the disk fills, Postgres stops accepting writes and the collector fails in a way that is tedious to diagnose. 60 GB is comfortable now and will not be forever.

`deploy/disk-check.sh`:

- Hourly via systemd timer
- Warn at 75%, critical at 90%
- Log to journald at warn/error level
- Report the Postgres volume specifically, not just the root filesystem

Record in the runbook: how to check current usage, how much a month of candles consumes in practice (measure it after a week rather than estimating), and how to add storage with this provider.

---

## 8. Verifying the deployment

The runbook must contain this checklist, and setup is not complete until every line passes on the actual host:

- [ ] `podman|docker compose ps` shows postgres, api, collector running
- [ ] Migrations applied automatically — no manual step (this was a real Phase 02 defect; do not reintroduce it)
- [ ] `/health` returns 200 over Tailscale, and is **unreachable** from the public IP
- [ ] `/internal/market/status` shows `ws_connected: true` and state `live` after backfill
- [ ] 1m `latest_age_seconds` stays under 120 across several checks
- [ ] `data_gaps` contains no unfilled rows, or only the known 2023-03-24 outage
- [ ] `reboot` brings the whole stack back with no manual intervention
- [ ] Backup runs, produces a plausibly-sized dump, and a test restore succeeds
- [ ] Disk check runs and reports
- [ ] `ufw status` shows only SSH open
- [ ] Password SSH login is refused

---

## 9. The 24-hour test

This is the one thing that could not be tested anywhere else, and it is the reason a real host matters.

Binance closes WebSocket connections roughly every 24 hours. Phase 02 fixed a defect where confirmed candles were dropped from the buffer on disconnect — the fix is verified against fakes but has never met the real exchange.

After deployment, leave it running at least 48 hours, then check:

- `reconnect_count` is at least 1
- No new unfilled gaps appeared around the reconnect timestamps
- Logs show the reconnect at info level, with backfill following it

Record the result in `deploy/README.md`. If a gap did appear at a reconnect, stop and report it — that is a data-integrity defect and it outranks anything in Phase 06.

---

## Out of scope

- Nginx, Caddy, TLS certificates, public domain — Tailscale covers access for now; revisit at Phase 08
- CI/CD pipelines
- Prometheus, Grafana, external alerting
- Running strategies or backtests on this host
- Any code that places orders

---

## How to start

Summarise the plan as a numbered list and wait for approval. Where a step must be run by hand on the VPS rather than scripted, say so plainly in the runbook — a runbook that pretends to be more automated than it is will strand someone at 2am.
