#!/usr/bin/env bash
# Insert a handful of sample links against a running linkforge server.
# Usage: BASE_URL=http://localhost:8080 ./scripts/seed.sh
set -euo pipefail
BASE_URL="${BASE_URL:-http://localhost:8080}"

shorten() {
  curl -s -X POST "$BASE_URL/shorten" \
    -H 'Content-Type: application/json' \
    -d "$1"
  echo
}

echo "Seeding sample links against $BASE_URL ..."
shorten '{"url":"https://example.com"}'
shorten '{"url":"https://go.dev/doc/effective_go"}'
shorten '{"url":"https://www.postgresql.org/docs/"}'
shorten '{"url":"https://github.com/shaurya703/linkforge","alias":"lf"}'
shorten '{"url":"https://example.org/limited-offer","ttl_seconds":3600}'
echo "Done. Try: curl -i $BASE_URL/lf"
