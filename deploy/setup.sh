#!/usr/bin/env bash
#
# Provisioning for the BTC/USDT collector host. Ubuntu 24.04, 2 vCPU / 4 GB.
#
# Every step is idempotent: a half-finished run followed by a re-run converges
# rather than compounding. That is not a nicety — provisioning is interrupted
# by dropped SSH sessions more often than it completes cleanly, and a script
# that cannot be re-run turns that into a rebuild.
#
# Usage:
#   sudo ./setup.sh base        # user, swap, UTC, unattended-upgrades, ufw
#   sudo ./setup.sh docker      # Docker from Docker's apt repository
#   sudo ./setup.sh tailscale   # install; joining the tailnet is interactive
#        ./setup.sh app         # /opt/btcusd checkout and .env scaffold
#   sudo ./setup.sh systemd     # install and enable units and timers
#   sudo ./setup.sh harden-ssh  # key-only SSH — READ THE WARNING FIRST
#   sudo ./setup.sh all         # base + docker + tailscale, then stops
#
# `all` stops before `app` because the checkout needs a deploy token and a
# different user, and it excludes harden-ssh entirely. Locking yourself out of
# a fresh VPS is a rebuild, so that step is opt-in, guarded, and never runs by
# surprise.
#
# See deploy/README.md for the order these are meant to be run in and for the
# steps that are not scripted at all.

set -euo pipefail

readonly DEPLOY_USER="${DEPLOY_USER:-btcusd}"
readonly APP_DIR="${APP_DIR:-/opt/btcusd}"
readonly REPO_URL="${REPO_URL:-https://github.com/spioneracorei8/btcusd-trading-platform.git}"
readonly SWAP_FILE="/swapfile"
readonly SWAP_SIZE_MB=2048
readonly UNIT_DIR="/etc/systemd/system"

# Where the deploy token is kept. Mode 600, owned by the deploy user, and
# outside the repository so a `git clean` cannot remove it.
readonly CRED_FILE="/home/${DEPLOY_USER}/.git-credentials"

log()  { printf '[setup] %s\n' "$*"; }
warn() { printf '[setup] WARNING: %s\n' "$*" >&2; }
die()  { printf '[setup] ERROR: %s\n' "$*" >&2; exit 1; }

need_root() {
	[[ ${EUID} -eq 0 ]] || die "this step needs root; re-run with sudo"
}

# ---------------------------------------------------------------------------
# base
# ---------------------------------------------------------------------------

step_base() {
	need_root

	log "creating the deploy user"
	if id -u "${DEPLOY_USER}" >/dev/null 2>&1; then
		log "  user ${DEPLOY_USER} already exists"
	else
		adduser --disabled-password --gecos "" "${DEPLOY_USER}"
	fi
	# usermod -aG is additive and safe to repeat.
	usermod -aG sudo "${DEPLOY_USER}"

	# The account is created with --disabled-password, so there is no password
	# to type at a sudo prompt and sudo would simply fail. Passwordless sudo is
	# what Ubuntu's own cloud images give their default user, and the reasoning
	# is the same here: SSH is key-only, so anyone who can reach a shell to run
	# sudo already holds the key. A password on top guards a narrow case and
	# would make every step below interactive.
	local sudoers="/etc/sudoers.d/90-${DEPLOY_USER}"
	printf '%s ALL=(ALL) NOPASSWD:ALL\n' "${DEPLOY_USER}" >"${sudoers}.tmp"
	chmod 440 "${sudoers}.tmp"
	# Never install an unvalidated sudoers file: a syntax error in that
	# directory breaks sudo for everyone, including the account that would fix
	# it.
	if visudo -cqf "${sudoers}.tmp"; then
		mv "${sudoers}.tmp" "${sudoers}"
	else
		rm -f "${sudoers}.tmp"
		die "generated sudoers file failed validation; nothing was installed"
	fi

	# Created here, as root, so the app step needs no privileges of its own.
	install -d -m 755 -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" "${APP_DIR}"

	# Copy root's authorized_keys across, so the key that got you in works for
	# the deploy user too. Without this, harden-ssh would lock everyone out the
	# moment PermitRootLogin goes off.
	if [[ -f /root/.ssh/authorized_keys ]]; then
		install -d -m 700 -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" "/home/${DEPLOY_USER}/.ssh"
		install -m 600 -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" \
			/root/.ssh/authorized_keys "/home/${DEPLOY_USER}/.ssh/authorized_keys"
		log "  copied root's authorized_keys to ${DEPLOY_USER}"
	else
		warn "root has no authorized_keys; make sure ${DEPLOY_USER} has one before harden-ssh"
	fi

	log "setting the timezone to UTC"
	# Every timestamp in this system is UTC (CLAUDE.md §4). A host on
	# Asia/Bangkok makes journald and application logs disagree by seven hours,
	# which is discovered at the worst possible moment.
	timedatectl set-timezone UTC

	log "adding ${SWAP_SIZE_MB} MB of swap"
	if swapon --show=NAME --noheadings | grep -qx "${SWAP_FILE}"; then
		log "  ${SWAP_FILE} already active"
	else
		if [[ ! -f ${SWAP_FILE} ]]; then
			# fallocate is instant but produces a sparse file on some
			# filesystems, which swapon rejects. dd is slower and always works.
			fallocate -l "${SWAP_SIZE_MB}M" "${SWAP_FILE}" 2>/dev/null ||
				dd if=/dev/zero of="${SWAP_FILE}" bs=1M count="${SWAP_SIZE_MB}" status=none
		fi
		chmod 600 "${SWAP_FILE}"
		mkswap "${SWAP_FILE}" >/dev/null
		swapon "${SWAP_FILE}"
	fi
	if ! grep -qF "${SWAP_FILE}" /etc/fstab; then
		printf '%s none swap sw 0 0\n' "${SWAP_FILE}" >>/etc/fstab
	fi
	# Swap here is insurance against an OOM kill taking the collector down, not
	# a tier of memory to run in. A low swappiness keeps the kernel from using
	# it while RAM is still available.
	sysctl -q -w vm.swappiness=10
	printf 'vm.swappiness=10\n' >/etc/sysctl.d/99-btcusd-swappiness.conf

	log "installing unattended-upgrades"
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	apt-get install -y -qq unattended-upgrades ca-certificates curl git jq >/dev/null
	# Enables the security origin and the daily timers. Writing the file
	# directly rather than using dpkg-reconfigure keeps this non-interactive
	# and re-runnable.
	cat >/etc/apt/apt.conf.d/20auto-upgrades <<-'EOF'
		APT::Periodic::Update-Package-Lists "1";
		APT::Periodic::Unattended-Upgrade "1";
	EOF
	systemctl enable --now unattended-upgrades >/dev/null 2>&1 || true

	log "configuring the firewall"
	apt-get install -y -qq ufw >/dev/null
	ufw --force default deny incoming >/dev/null
	ufw --force default allow outgoing >/dev/null
	ufw allow OpenSSH >/dev/null
	# Traffic arriving on the tailnet interface. This is not a public opening:
	# nothing reaches tailscale0 without being an authenticated peer, and the
	# public interface stays closed. Without this rule, "deny incoming" also
	# denies the tailnet and /health is unreachable from anywhere at all.
	ufw allow in on tailscale0 >/dev/null
	ufw --force enable >/dev/null
	ufw status verbose

	log "base setup complete"
}

# ---------------------------------------------------------------------------
# docker
# ---------------------------------------------------------------------------

step_docker() {
	need_root

	# Ubuntu's docker.io package lags and ships no compose v2 plugin. The
	# runbook has to be able to state one command that works, so this uses
	# Docker's own repository.
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
		log "docker and the compose plugin are already installed"
	else
		log "installing docker from Docker's apt repository"
		export DEBIAN_FRONTEND=noninteractive
		install -m 0755 -d /etc/apt/keyrings
		curl -fsSL https://download.docker.com/linux/ubuntu/gpg |
			gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
		chmod a+r /etc/apt/keyrings/docker.gpg

		local codename
		codename="$(. /etc/os-release && echo "${VERSION_CODENAME}")"
		cat >/etc/apt/sources.list.d/docker.list <<-EOF
			deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${codename} stable
		EOF

		apt-get update -qq
		apt-get install -y -qq \
			docker-ce docker-ce-cli containerd.io \
			docker-buildx-plugin docker-compose-plugin >/dev/null
	fi

	usermod -aG docker "${DEPLOY_USER}"
	systemctl enable --now docker >/dev/null

	docker --version
	docker compose version
	log "docker ready — ${DEPLOY_USER} must log out and back in for the group to apply"
}

# ---------------------------------------------------------------------------
# tailscale
# ---------------------------------------------------------------------------

step_tailscale() {
	need_root

	if command -v tailscale >/dev/null 2>&1; then
		log "tailscale already installed"
	else
		log "installing tailscale"
		curl -fsSL https://tailscale.com/install.sh | sh
	fi
	systemctl enable --now tailscaled >/dev/null

	if tailscale status >/dev/null 2>&1; then
		log "already joined to a tailnet:"
		tailscale ip -4
	else
		log ""
		log "Not joined yet. Run this and follow the printed URL:"
		log ""
		log "    sudo tailscale up --ssh=false"
		log ""
		# --ssh=false is deliberate. Tailscale SSH would make the only way into
		# this machine depend on a third party being reachable; key-based SSH
		# on the public interface stays as the path that does not.
		log "Then put the address 'tailscale ip -4' prints into ${APP_DIR}/.env"
		log "as TAILSCALE_IP, and record the tailnet name in deploy/README.md."
	fi
}

# ---------------------------------------------------------------------------
# app
# ---------------------------------------------------------------------------

step_app() {
	[[ ${EUID} -ne 0 ]] || die "run this step as ${DEPLOY_USER}, not root — the checkout must not be root-owned"
	[[ $(id -un) == "${DEPLOY_USER}" ]] || warn "expected to run as ${DEPLOY_USER}, running as $(id -un)"

	if [[ ! -d ${APP_DIR}/.git ]]; then
		log "cloning into ${APP_DIR}"

		local token="${GITHUB_TOKEN:-}"
		if [[ -z ${token} ]]; then
			# -s so it does not land in the scrollback of a shared terminal.
			read -rsp "GitHub read-only deploy token (input hidden): " token
			printf '\n'
		fi
		[[ -n ${token} ]] || die "no token given"

		# The token goes in a 600 credentials file rather than in the remote
		# URL: a URL-embedded token is printed by `git remote -v`, copied into
		# every error message, and committed to .git/config where a later
		# `git config --list` dump will expose it.
		install -m 600 /dev/null "${CRED_FILE}"
		printf 'https://x-access-token:%s@github.com\n' "${token}" >"${CRED_FILE}"
		git config --global credential.helper "store --file=${CRED_FILE}"

		# The base step created APP_DIR owned by this user, so nothing here
		# needs privileges. git clone accepts an existing empty directory.
		[[ -d ${APP_DIR} ]] || die "${APP_DIR} does not exist; run the base step first"
		git clone "${REPO_URL}" "${APP_DIR}"
	else
		log "updating ${APP_DIR}"
		# --ff-only: this host never has local commits, and a merge commit
		# appearing here would mean something is wrong that a pull must not
		# paper over.
		git -C "${APP_DIR}" pull --ff-only
	fi

	if [[ -f ${APP_DIR}/.env ]]; then
		log ".env already exists — leaving it alone"
	else
		log "creating .env from .env.example"
		install -m 600 "${APP_DIR}/.env.example" "${APP_DIR}/.env"

		local password
		password="$(openssl rand -base64 32 | tr -d '/+=' | head -c 32)"
		# The development password must not follow the database onto a host
		# that stays up for months.
		sed -i "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${password}|" "${APP_DIR}/.env"
		sed -i "s|^APP_ENV=.*|APP_ENV=prod|" "${APP_DIR}/.env"
		sed -i "s|^LOG_LEVEL=.*|LOG_LEVEL=info|" "${APP_DIR}/.env"
		sed -i "s|^CONTAINER_ENGINE=.*|CONTAINER_ENGINE=docker|" "${APP_DIR}/.env"
		# DATABASE_URL in .env is the *host* connection string, used by
		# make migrate-up and by backup.sh. The containers build their own.
		sed -i "s|^DATABASE_URL=.*|DATABASE_URL=postgres://trading:${password}@localhost:5432/btcusd?sslmode=disable|" \
			"${APP_DIR}/.env"

		chmod 600 "${APP_DIR}/.env"
		log ""
		log "A PostgreSQL password was generated and written to ${APP_DIR}/.env."
		log "TAILSCALE_IP is still empty — the stack will refuse to start until"
		log "it is set. See deploy/README.md §5."
	fi

	grep -q '^TAILSCALE_IP=' "${APP_DIR}/.env" ||
		printf '\n# This host'"'"'s tailnet address; `tailscale ip -4`.\nTAILSCALE_IP=\n' >>"${APP_DIR}/.env"
}

# ---------------------------------------------------------------------------
# systemd
# ---------------------------------------------------------------------------

step_systemd() {
	need_root

	local src="${APP_DIR}/deploy/systemd"
	[[ -d ${src} ]] || die "${src} not found; run the app step first"

	log "installing units"
	install -m 644 "${src}"/*.service "${src}"/*.timer "${UNIT_DIR}/"

	systemctl daemon-reload
	systemctl enable btcusd.service >/dev/null
	systemctl enable --now btcusd-backup.timer >/dev/null
	systemctl enable --now btcusd-disk-check.timer >/dev/null

	log "units installed. Start the stack with: sudo systemctl start btcusd"
	systemctl list-timers --no-pager 'btcusd-*' || true
}

# ---------------------------------------------------------------------------
# harden-ssh
# ---------------------------------------------------------------------------

step_harden_ssh() {
	need_root

	local keys="/home/${DEPLOY_USER}/.ssh/authorized_keys"

	# The guard. Disabling password authentication with no key installed turns
	# a VPS into a brick that has to be rebuilt from the provider's panel.
	[[ -s ${keys} ]] ||
		die "${keys} is missing or empty. Install your public key for ${DEPLOY_USER} first — refusing to disable password login."

	local count
	count="$(grep -cvE '^\s*(#|$)' "${keys}" || true)"
	log "${DEPLOY_USER} has ${count} authorized key(s)"

	cat <<-EOF

		  ============================================================
		  STOP. Before continuing:

		    1. Open a SECOND terminal, leave this one connected.
		    2. From it, run:  ssh ${DEPLOY_USER}@<this-host>
		    3. Confirm you get a shell WITHOUT typing a password.

		  If that second session does not work, this step will lock
		  you out and the machine must be rebuilt. Nothing below can
		  undo it from outside.
		  ============================================================

	EOF

	read -rp "Type 'yes' if the second session works: " answer
	[[ ${answer} == "yes" ]] || die "aborted; nothing was changed"

	# A drop-in rather than an edit to sshd_config: re-running overwrites one
	# small file with known content, where sed against the main config would
	# accumulate duplicate directives on every run.
	local dropin="/etc/ssh/sshd_config.d/99-btcusd-hardening.conf"
	cat >"${dropin}" <<-'EOF'
		# Managed by deploy/setup.sh. See deploy/README.md §2.
		PasswordAuthentication no
		PermitRootLogin no
		KbdInteractiveAuthentication no
		ChallengeResponseAuthentication no
	EOF
	chmod 644 "${dropin}"

	# Validate before reloading. A syntax error plus a reload is the other way
	# to lose access.
	sshd -t || { rm -f "${dropin}"; die "sshd rejected the config; the drop-in was removed and nothing changed"; }

	systemctl reload ssh 2>/dev/null || systemctl reload sshd
	log "password authentication and root login are now disabled"
	log "KEEP THIS SESSION OPEN until you have confirmed a new one still works."
}

# ---------------------------------------------------------------------------

main() {
	case "${1:-}" in
	base) step_base ;;
	docker) step_docker ;;
	tailscale) step_tailscale ;;
	app) step_app ;;
	systemd) step_systemd ;;
	harden-ssh) step_harden_ssh ;;
	all)
		step_base
		step_docker
		step_tailscale
		log ""
		log "Now, as ${DEPLOY_USER}:  ${APP_DIR}/deploy/setup.sh app"
		log "then, as root:          ${APP_DIR}/deploy/setup.sh systemd"
		log "and last, deliberately: ${APP_DIR}/deploy/setup.sh harden-ssh"
		;;
	*)
		sed -n '2,30p' "$0"
		exit 1
		;;
	esac
}

main "$@"
