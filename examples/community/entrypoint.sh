#!/bin/sh
set -e

DASHBOARD_URL="${MUVEE_DASHBOARD_URL:-${MUVEE_API_BASE}/projects}"

cat > /usr/share/nginx/html/env.js <<EOF
window.MUVEE_API_BASE = "${MUVEE_API_BASE}";
window.MUVEE_DASHBOARD_URL = "${DASHBOARD_URL}";
EOF

exec nginx -g "daemon off;"
