# VPS runbook

The collector host has one job: **stay up and keep the candle series
complete.** Everything here serves that.

This machine does not run strategies, does not run backtests, and does not
place orders. Backtesting happens on the developer machine, against a copy of
this data. `CLAUDE.md` §1 applies here as everywhere else: there is no order,
trade or withdrawal code path anywhere in this system.

Written to be followed months from now, by someone who has forgotten all of
it. Steps that must be typed by a human on the VPS are marked **[by hand]**.
Nothing here pretends to be more automated than it is.

---

## 1. Facts about this deployment

Fill these in during setup. They are the things that are painful to
reconstruct later.

| | |
|---|---|
| Provider | VisperHost Cloud VPS 02 |
| Spec | 2 vCPU / 4 GB RAM / 60 GB SSD |
| OS | Ubuntu 24.04 |
| Public IPv4 | `________________` |
| Container engine | **Docker** (from Docker's apt repository, with the compose v2 plugin) |
| Deploy user | `btcusd` |
| Checkout | `/opt/btcusd` |
| Secrets | `/opt/btcusd/.env`, mode 600, never committed |
| **PostgreSQL volume** | **`btcusd-trading-platform_pgdata`** |
| Backups | `/var/backups/btcusd/{daily,weekly}` |
| Tailnet name | `________________` |
| Tailscale IPv4 | `________________` |
| Deployed on | `________________` |

**The volume is the thing that must survive everything else.** The checkout can
be re-cloned, the images rebuilt, the host rebuilt. Two years of 1m candles
cannot be re-fetched — Binance serves recent klines, not deep history on
demand. `make prod-down` keeps it; adding `-v` to a compose `down` destroys it.
Never type that on this host.

### Why Docker here when development uses podman

The runbook has to be able to state one command that works. Docker's own apt
repository ships a current engine and the compose v2 plugin together, and
`restart: unless-stopped` is honoured by a daemon that is already running at
boot. Under rootless podman the same policy needs `podman-restart.service` plus
lingering enabled for the user, and if either is missed the stack silently does
not come back after a reboot — which is the one failure this host cannot
tolerate. The development machine is unaffected; `make engine` reports whichever
is in use.

---

## 2. First-time setup

Run the steps in this order. `setup.sh` is idempotent: if a step is
interrupted, re-run it.

### 2.1 Get in and provision the base

**[by hand]** Copy the provisioning script up from your own machine first. It
is one file, and doing it this way means the host needs no repository access
until §2.3 — which matters if the repository is private, because the token that
would grant it does not exist on the VPS yet.

```bash
# from your local checkout:
scp deploy/setup.sh root@<public-ip>:/root/setup.sh

# then on the VPS, as root:
ssh root@<public-ip>
chmod +x /root/setup.sh
/root/setup.sh base
```

That creates the `btcusd` user (key-only, with passwordless sudo — see
[ADR 0017](../docs/decisions/0017-vps-deployment.md)), copies root's authorized
key to it, and creates `/opt/btcusd` owned by it. It sets the timezone to
**UTC** — not Asia/Bangkok. Every timestamp in this system is UTC, and a host
on local time makes journald disagree with the application logs by seven hours,
which is discovered at the worst possible moment. It then adds 2 GB of swap,
enables `unattended-upgrades`, and configures `ufw` to deny incoming except SSH
and the tailnet interface.

### 2.2 Docker and Tailscale

```bash
/root/setup.sh docker
/root/setup.sh tailscale
```

**[by hand]** Joining the tailnet is interactive — it prints a URL you must
open in a browser and authenticate:

```bash
sudo tailscale up --ssh=false
tailscale ip -4          # write this into the table in §1
```

`--ssh=false` is deliberate. Tailscale SSH would make the only way into this
machine depend on a third party being reachable. Key-based SSH on the public
interface stays as the path that does not.

### 2.3 The application

**[by hand]** Log out and back in **as `btcusd`** — the `docker` group
membership does not apply to an existing session.

You need a **GitHub read-only deploy token** before this step: a fine-grained
personal access token with `Contents: read` on this repository only. Create it
at <https://github.com/settings/tokens>, and set an expiry — a token that never
expires is one nobody ever revokes.

```bash
ssh btcusd@<public-ip>
sudo install -m 755 -o btcusd /root/setup.sh /tmp/setup.sh
/tmp/setup.sh app                    # prompts for the token
```

This clones into the empty `/opt/btcusd` the base step created; from then on
the checked-out `/opt/btcusd/deploy/setup.sh` is the copy to use. It also
creates `/opt/btcusd/.env` from `.env.example` with a **freshly generated
PostgreSQL password** — the development password must not follow the database
onto a host that stays up for months.

The token is stored in `~/.git-credentials` at mode 600 rather than in the
remote URL, so `git remote -v` does not print it and it does not end up in
`.git/config`.

**[by hand]** Set the Tailscale address in the environment file:

```bash
tailscale ip -4                # e.g. 100.72.14.3
nano /opt/btcusd/.env          # TAILSCALE_IP=100.72.14.3
```

The stack **refuses to start** while `TAILSCALE_IP` is empty. That is on
purpose: an empty value inside a port mapping expands to `":8080:8080"`, which
binds every interface including the public one, and nothing in
`docker compose ps` would show it. Failing loudly is the only safe behaviour.

### 2.4 systemd

```bash
sudo /opt/btcusd/deploy/setup.sh systemd
sudo systemctl start btcusd
```

That installs and enables `btcusd.service` (the stack, on boot),
`btcusd-backup.timer` (daily 03:17 UTC) and `btcusd-disk-check.timer`
(hourly).

The first start builds three images on a 2 vCPU box. It is not fast; the unit
has no start timeout for that reason. Watch it with
`journalctl -u btcusd -f`.

### 2.5 Harden SSH — last, and deliberately

> **This is the step that can lock you out permanently.** A fresh VPS with no
> way in is a rebuild. Read the whole step before starting it.

```bash
sudo /opt/btcusd/deploy/setup.sh harden-ssh
```

The script refuses to run unless `/home/btcusd/.ssh/authorized_keys` exists and
is non-empty, then stops and makes you confirm.

**[by hand] Before answering the prompt:**

1. Leave the current session **open**.
2. Open a **second terminal**.
3. Run `ssh btcusd@<public-ip>` and confirm you get a shell **without typing a
   password**.
4. Only then type `yes`.

**[by hand] After it finishes:** keep both sessions open and open a *third* to
confirm login still works. Only then close the others.

---

## 3. Day to day

The stack is managed by systemd. Prefer these to raw compose commands, so what
systemd believes matches what is running:

```bash
sudo systemctl status btcusd        # is it up
sudo systemctl restart btcusd       # after changing .env
sudo systemctl stop btcusd          # keeps the volume
journalctl -u btcusd -f             # unit-level logs
```

Container logs, health, and updates:

```bash
cd /opt/btcusd
make prod-ps
make prod-logs                      # follow all services
make prod-config                    # what the containers will actually receive
```

> **Every compose command needs `--env-file`, and always the one at the
> repository root.**
>
> Compose looks for `.env` in the directory holding the compose file —
> `deploy/` — not at the repository root. There is no `deploy/.env`, so a bare
> `docker compose ...` reads no environment file at all.
>
> That used to mean it silently fell back to defaults baked into the compose
> file, and editing `/opt/btcusd/.env` changed nothing with no indication why.
> The defaults for anything that decides what is collected or what the numbers
> mean have since been removed, so a bare command now **fails** naming the
> variable it could not resolve. That is the intended outcome, not a bug.
>
> The `make prod-*` targets and `btcusd.service` both pass the file explicitly.
> Prefer them. When a raw command is genuinely needed — reaching one service —
> spell it out in full:
>
> ```bash
> cd /opt/btcusd
> docker compose --env-file /opt/btcusd/.env \
>   -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml \
>   logs -f collector
> ```

Deploying a new version:

```bash
cd /opt/btcusd
git pull --ff-only
sudo systemctl restart btcusd       # rebuilds and recreates
```

Migrations apply automatically. The `migrate` service runs to completion before
`api` and `collector` start, and goose is idempotent. **There is no manual
migration step and there must not be one** — a missing table on a VPS is far
less obvious than it is locally, which is exactly how it was found in phase 02.

### Reaching the API

Over the tailnet, from any device on it:

```bash
curl http://<tailscale-ip>:8080/health
curl http://<tailscale-ip>:8080/internal/market/status | jq
```

The status response nests the collector's own fields under `collector`, and
reports freshness per timeframe under `timeframes`:

```bash
S=http://<tailscale-ip>:8080/internal/market/status

curl -s $S | jq '.collector | {state, running, ws_connected, reconnect_count, heartbeat_age_seconds}'
curl -s $S | jq '.timeframes[] | {timeframe, latest_age_seconds, unfilled_gaps}'
curl -s $S | jq '.stale'    # connected but not advancing — the one worth paging on
```

`stale` is `null` rather than `false` outside the live state. During a backfill
an old candle is progress, and a `false` there would read as an all-clear.

From the public IP this must fail. That is the point.

### Reaching the database

PostgreSQL is published on `127.0.0.1` only — not on the tailnet. Use an SSH
tunnel:

```bash
ssh -L 5432:127.0.0.1:5432 btcusd@<public-ip>
psql "postgres://trading:<password>@localhost:5432/btcusd"
```

> This is stricter than the deployment spec, which grouped PostgreSQL with the
> API as "reached over Tailscale". Nothing on the tailnet needs SQL — the
> mobile app and the phase 08 API both go through HTTP — so the database stays
> behind a tunnel and the surface stays smaller. The extra flag when a human
> wants a `psql` prompt is a fair price.

Adminer, if you want to browse by hand, is behind the `tools` profile and also
loopback-only: `make adminer`, then tunnel port 8081. Stop it again with
`make adminer-stop`. It does not run as part of the stack and should not be
left running.

---

## 4. Backups

`data_gaps` exists because the system records what it could not fetch. A lost
database is the same problem several orders of magnitude larger, except nothing
records it and nothing can backfill it.

`btcusd-backup.timer` runs `deploy/backup.sh` daily at 03:17 UTC. It writes a
gzipped `pg_dump` to `/var/backups/btcusd/daily/`, hard-links Sunday's copy into
`weekly/`, and keeps **7 daily and 4 weekly**.

```bash
sudo systemctl start btcusd-backup      # run one now
journalctl -u btcusd-backup -n 50       # what happened
systemctl list-timers 'btcusd-*'        # when next
ls -lh /var/backups/btcusd/daily/
```

The script fails loudly rather than writing a bad backup quietly. It refuses a
dump that is:

- **below an absolute floor** (10 KB) — catches an empty or failed dump; the
  file is deleted so it cannot be mistaken for a backup
- **not a valid gzip stream** — catches truncation from a full disk or a killed
  container, checked with `gzip -t`, which reads the whole stream
- **under 50% of the previous dump** — catches a database that still dumps
  cleanly but has lost rows. This one is **kept**, not deleted: if the data
  really is gone, the shrunken dump may be the only copy left.

A failure marks the unit failed, which shows in `systemctl list-timers` and
`systemctl status btcusd-backup`.

### Restoring

**An untested backup is a hypothesis.** Test it into a scratch database, which
never touches the live one:

```bash
sudo /opt/btcusd/deploy/restore-test.sh              # newest daily dump
sudo /opt/btcusd/deploy/restore-test.sh /var/backups/btcusd/weekly/btcusd-....sql.gz
```

It restores into `btcusd_restore_test`, compares row counts for `candles`,
`signals`, `data_gaps` and `collector_status` against the live database, and
drops the scratch database afterwards.

> Run this **once during setup** and again after anything that changes the
> database: a PostgreSQL major version bump, a TimescaleDB upgrade, or a
> migration large enough to be worth worrying about.

If the collector is running, `candles` will be a few rows ahead in the live
database by the time the comparison runs. **A small positive difference is
normal. A large one, or a negative one, is not.**

**Restoring for real** — only when the live database is lost or corrupt:

```bash
# 1. Stop everything that writes.
sudo systemctl stop btcusd

# 2. Bring up only PostgreSQL.
cd /opt/btcusd
docker compose --env-file /opt/btcusd/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.prod.yml up -d postgres

# 3. Recreate the database.
docker compose --env-file /opt/btcusd/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.prod.yml exec -T postgres \
  psql -U trading -d postgres -c 'DROP DATABASE btcusd;' -c 'CREATE DATABASE btcusd;'

# 4. TimescaleDB needs this around the restore. Without it the catalog and the
#    chunks disagree, the restore appears to succeed, and the hypertable is
#    subtly broken.
docker compose --env-file /opt/btcusd/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.prod.yml exec -T postgres \
  psql -U trading -d btcusd -c 'CREATE EXTENSION IF NOT EXISTS timescaledb;' \
                             -c 'SELECT timescaledb_pre_restore();'

gunzip -c /var/backups/btcusd/daily/btcusd-<stamp>.sql.gz | \
  docker compose --env-file /opt/btcusd/.env -f deploy/docker-compose.yml \
    -f deploy/docker-compose.prod.yml exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U trading -d btcusd

docker compose --env-file /opt/btcusd/.env -f deploy/docker-compose.yml \
  -f deploy/docker-compose.prod.yml exec -T postgres \
  psql -U trading -d btcusd -c 'SELECT timescaledb_post_restore();'

# 5. Start everything. The collector backfills the gap between the dump and now
#    on its own — that is what the gap detector is for.
sudo systemctl start btcusd
```

Then check `/internal/market/status` and `data_gaps`.

---

## 5. Disk

If the disk fills, PostgreSQL stops accepting writes and the collector fails in
a way that reads like a database problem rather than a disk one.

`btcusd-disk-check.timer` runs hourly and logs to journald at the matching
severity — warning at 75%, error at 90%:

```bash
sudo /opt/btcusd/deploy/disk-check.sh          # right now
journalctl -u btcusd-disk-check -p warning     # only what mattered
journalctl -u btcusd-disk-check -n 20          # the last few checks
```

It reports the root filesystem, the filesystem holding the PostgreSQL volume
(they may differ), the size of the volume directory, and the size of the backup
directory. Checking only `/` would miss a separate disk for the volume filling
while `/` stays comfortable.

### How fast does it actually grow

**[by hand] Measure this rather than estimating it.** Record the volume size a
week apart and put the numbers here:

| date | postgres volume | backups | note |
|---|---|---|---|
| `________` | `________` | `________` | day 1 |
| `________` | `________` | `________` | day 7 |
| `________` | `________` | `________` | day 30 |

```bash
sudo du -sh "$(docker volume inspect -f '{{.Mountpoint}}' btcusd-trading-platform_pgdata)"
sudo du -sh /var/backups/btcusd
```

Per-table detail, when the total starts mattering:

```sql
SELECT hypertable_name, pg_size_pretty(hypertable_size(format('%I', hypertable_name)::regclass))
  FROM timescaledb_information.hypertables;
```

**Growth per month (measured): `________`**

### Adding storage

**[by hand]** VisperHost resizes a VPS from the client panel; the disk grows
with the plan rather than as a separately attached volume. After a resize:

```bash
lsblk                                  # confirm the larger device
sudo growpart /dev/vda 1               # extend the partition
sudo resize2fs /dev/vda1               # then the filesystem
df -h /
```

Take a backup and copy it **off the host** before resizing. Verify the numbers
against the provider's own documentation at the time — this is written from
their current panel and providers change.

---

## 6. Verification checklist

Setup is not complete until every line passes **on the actual host**. Date it
and keep it.

Checked on: `________________`

- [ ] `make prod-ps` shows `postgres`, `api`, `collector` running
- [ ] Migrations applied automatically, with no manual step
- [ ] `/health` returns 200 over Tailscale
- [ ] `/health` is **unreachable** from the public IP
- [ ] `/internal/market/status` shows `ws_connected: true` and state `live` after backfill
- [ ] 1m `latest_age_seconds` stays under 120 across several checks
- [ ] `data_gaps` has no unfilled rows, or only the known 2023-03-24 outage
- [ ] `reboot` brings the whole stack back with no manual intervention
- [ ] Backup runs, produces a plausibly-sized dump, and `restore-test.sh` passes
- [ ] Disk check runs and reports
- [ ] `ufw status` shows only SSH open to the internet
- [ ] Password SSH login is refused

Commands for the ones that are not obvious:

```bash
# From a device on the tailnet:
curl -s http://<tailscale-ip>:8080/health
curl -s http://<tailscale-ip>:8080/internal/market/status |
  jq '{state: .collector.state, ws: .collector.ws_connected, stale,
       tf: [.timeframes[] | {timeframe, latest_age_seconds, unfilled_gaps}]}'

# From anywhere else — both must fail, not hang and not answer:
curl -m 5 http://<public-ip>:8080/health ; echo "exit=$?"
curl -m 5 http://<public-ip>:5432        ; echo "exit=$?"

# Unfilled gaps:
psql -c "SELECT * FROM data_gaps WHERE filled_at IS NULL ORDER BY gap_start;"

# Password login must be refused, not prompted:
ssh -o PreferredAuthentications=password -o PubkeyAuthentication=no \
    btcusd@<public-ip>

# The reboot test. Do it deliberately, while watching:
sudo reboot
# then, after it comes back:
systemctl status btcusd && make -C /opt/btcusd prod-ps
```

> `ufw status` will list an `ALLOW IN` rule on `tailscale0`. That is an
> interface rule, not a public port: nothing reaches that interface without
> being an authenticated tailnet peer. Without it, "default deny incoming"
> would also deny the tailnet and `/health` would be reachable from nowhere at
> all.

---

## 7. The 48-hour test

**This is the one thing that could not be tested anywhere else, and it is the
reason a real host matters.**

Binance closes WebSocket connections roughly every 24 hours. Phase 02 fixed a
defect where confirmed candles were dropped from the buffer on disconnect. That
fix is verified against fakes and **has never met the real exchange.**

Leave the collector running at least 48 hours, then check:

```bash
curl -s http://<tailscale-ip>:8080/internal/market/status |
  jq '.collector | {reconnect_count, last_connected_at, last_disconnected_at, last_disconnect_note}'

# Reconnects, and the backfill that should follow each one:
journalctl -u btcusd --since '48 hours ago' | grep -iE 'reconnect|backfill'

# Any gap that appeared, and when:
psql -c "SELECT * FROM data_gaps ORDER BY gap_start DESC LIMIT 20;"
```

Pass conditions:

- [ ] `reconnect_count` is at least 1
- [ ] No new unfilled gaps around the reconnect timestamps
- [ ] Logs show the reconnect at info level, with backfill following it

### Result

| | |
|---|---|
| Started | `________________` |
| Checked | `________________` |
| `reconnect_count` | `________________` |
| New gaps at reconnects | `________________` |
| Verdict | `________________` |

> **If a gap did appear at a reconnect, stop and report it.** That is a
> data-integrity defect, and it outranks anything in phase 06. A strategy
> evaluated on a series with holes in it is measuring the holes.

---

## 8. Troubleshooting

**The stack will not start after a reboot.**
Usually `tailscale0` had no address yet when Docker tried to publish the API on
it. `btcusd.service` waits up to 60 seconds for the address before starting; if
Tailscale needed longer, `sudo systemctl start btcusd` fixes it. If that
happens more than once, raise the loop count in the unit's `ExecStartPre`.

```bash
journalctl -u btcusd -b        # this boot
tailscale status
ip -4 addr show tailscale0
```

**Compose says a variable `is missing a value`.**
Working as designed. Either the variable is genuinely unset in
`/opt/btcusd/.env` — `TAILSCALE_IP` after a fresh install, see §2.3 — or the
command was run without `--env-file` and read no environment file at all. Check
which before editing anything: `make prod-config` prints what the containers
would actually receive.

**The collector is up but `latest_age_seconds` keeps climbing.**
The WebSocket is connected but not delivering, or the process is wedged behind
a database problem. Check `.collector.ws_connected`,
`.collector.heartbeat_age_seconds` and `.collector.last_disconnect_note` in
`/internal/market/status`, then the collector's own logs. `.stale` is the field
that combines the two: connected but not advancing. A restart is safe: candles
are upserted by `open_time`, so a reconnect and re-backfill cannot
double-count.

**`data_gaps` has unfilled rows.**
The gap detector found holes it could not backfill. Look at the range: if it
covers a Binance outage the data does not exist and never will. Otherwise check
whether the REST backfill is being rate-limited.

**Disk critical.**
Backups first (`/var/backups/btcusd`), then container logs. Logs are capped at
10 MB × 3 per service by the production overlay, so they should not be the
cause; if they are, the overlay is not being applied — check that both `-f`
flags are present.

**I need to start over on the database but keep everything else.**
`sudo systemctl stop btcusd` then
`docker volume rm btcusd-trading-platform_pgdata`.
Be certain. Two years of candles do not come back.

---

## 9. Deliberately not here

- **Nginx, Caddy, TLS, a public domain.** Tailscale covers access. Revisit at
  phase 08, when the mobile app needs a reachable API. `deploy/Caddyfile` is
  kept for that day and is not used by this deployment.
- **CI/CD.** Deployment is `git pull` and `systemctl restart`.
- **Prometheus, Grafana, external alerting.** The disk check logs to journald;
  `/internal/market/status` is the health surface. Alerting arrives with the
  notification work in phase 07.
- **Strategies and backtests.** They run on the developer machine. This host
  collects data.
- **Anything that places an order.** Not here, not later. `CLAUDE.md` §1, and
  `server/architecture_test.go` fails the build if an order, account or
  withdrawal endpoint appears anywhere in the source.

48-hour test: PASS
disconnect 2026-08-17T05:11:01Z (StatusGoingAway, after 48h54m)
reconnect 2026-08-17T05:11:03Z, 2.4s downtime
no new gaps