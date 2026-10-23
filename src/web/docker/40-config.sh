#!/bin/sh
# Runs from /docker-entrypoint.d before nginx starts.
#   API_BASE      address the browser uses for the API (default /api)
#   UPSTREAM      if set, nginx proxies /api/ to it (e.g. the KEDA interceptor service)
#   UPSTREAM_HOST Host header sent upstream; the interceptor routes on it
set -eu

API_BASE="${API_BASE:-/api}"
printf '{"apiBase":"%s"}\n' "$API_BASE" > /usr/share/nginx/html/config.json

if [ -n "${UPSTREAM:-}" ]; then
  # A variable in proxy_pass makes nginx resolve the name at request time, so it
  # still starts when the upstream does not exist yet (e.g. scaled to zero).
  NS=$(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf)
  cat > /etc/nginx/api-proxy.conf <<CONF
location /api/ {
  resolver ${NS} valid=10s;
  set \$upstream "${UPSTREAM}";
  rewrite ^/api/(.*)\$ /\$1 break;
  proxy_pass \$upstream;
  proxy_set_header Host ${UPSTREAM_HOST:-\$host};
  proxy_read_timeout 120s;
  proxy_send_timeout 120s;
}
CONF
else
  : > /etc/nginx/api-proxy.conf
fi
