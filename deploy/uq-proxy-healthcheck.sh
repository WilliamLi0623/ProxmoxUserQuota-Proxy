#!/usr/bin/env bash
# Liveness watchdog for uq-proxy (P6 HA). systemd's Restart= already recovers a
# process that *exits*; this catches the harder case of a process that is still
# "active" but wedged (deadlocked goroutine, stuck listener) and so never
# triggers Restart=. Driven by uq-proxy-health.timer.
#
# Only acts when the unit is active-but-unhealthy: if systemd already sees it
# dead/activating, Restart=always owns the recovery and we stay out of the way
# (avoids fighting systemd's own restart).
set -u
ADMIN_HEALTH="${UQ_HEALTH_URL:-http://127.0.0.1:9090/healthz}"

[ "$(systemctl is-active uq-proxy 2>/dev/null)" = "active" ] || exit 0

if curl -fsS --max-time 5 "$ADMIN_HEALTH" 2>/dev/null | grep -q '^ok'; then
  exit 0
fi

logger -t uq-proxy-health "uq-proxy active but $ADMIN_HEALTH not OK; restarting"
systemctl restart uq-proxy
