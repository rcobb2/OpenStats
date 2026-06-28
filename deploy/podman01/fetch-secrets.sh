#!/bin/bash
# Fetch secrets from Bitwarden Secrets Manager and write to the env file.
# Called by ExecStartPre in openstats-compose.service.
#
# Prereqs on podman01:
#   1. Install bws CLI: https://github.com/bitwarden/sdk/releases (Linux x86_64 static binary)
#      sudo install -m 755 bws /usr/local/bin/bws
#   2. Create /etc/openstats/bws-token (root:root 0600):
#      echo "BWS_ACCESS_TOKEN=<your-machine-account-token>" > /etc/openstats/bws-token
#      chmod 600 /etc/openstats/bws-token
#   3. In Bitwarden Secrets Manager, create secrets named exactly:
#      POSTGRES_PASSWORD, GRAFANA_PASSWORD
#
# BWS_ACCESS_TOKEN is injected via EnvironmentFile in the systemd unit.
set -euo pipefail

OUT=/run/openstats/secrets.env

bws run \
    --access-token "$BWS_ACCESS_TOKEN" \
    -- env \
    | grep -E '^(POSTGRES_PASSWORD|GRAFANA_PASSWORD)=' \
    > "$OUT"

chmod 600 "$OUT"

for var in POSTGRES_PASSWORD GRAFANA_PASSWORD; do
    if ! grep -q "^${var}=" "$OUT"; then
        echo "ERROR: secret '${var}' not found in Bitwarden — check secret name matches exactly" >&2
        exit 1
    fi
done
