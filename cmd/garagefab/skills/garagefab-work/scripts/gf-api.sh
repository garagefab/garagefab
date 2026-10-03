#!/bin/sh
# gf-api.sh - Secure API client helper for the garagefab-work skill (HND-4, HND-5, SEC-3, SEC-4).
#
# Permitted operations (defense-in-depth):
#   - GET  /api/jobs/*
#   - POST /api/jobs/<id>/clarification
# All other methods and paths are rejected with exit code 2.

set -e

# Require curl
if ! command -v curl >/dev/null 2>&1; then
  echo "error: curl is required but not installed" >&2
  exit 1
fi

METHOD="$1"
ENDPOINT="$2"
BODY="$3"

if [ -z "$METHOD" ] || [ -z "$ENDPOINT" ]; then
  echo "usage: $0 <METHOD> <ENDPOINT> [JSON_BODY]" >&2
  exit 1
fi

# Normalize method to uppercase
METHOD=$(echo "$METHOD" | tr '[:lower:]' '[:upper:]')

# Defense-in-depth gate (HND-5): only permit GET /api/jobs* and POST /api/jobs/*/clarification
case "$METHOD" in
  GET)
    case "$ENDPOINT" in
      /api/jobs|/api/jobs/*)
        ;;
      *)
        echo "refused: not permitted by garagefab-work" >&2
        exit 2
        ;;
    esac
    ;;
  POST)
    case "$ENDPOINT" in
      /api/jobs/*/clarification)
        ;;
      *)
        echo "refused: not permitted by garagefab-work" >&2
        exit 2
        ;;
    esac
    ;;
  *)
    echo "refused: not permitted by garagefab-work" >&2
    exit 2
    ;;
esac

# Resolve Garagefab configuration
GF_HOME="${GARAGEFAB_HOME:-$HOME/.garagefab}"
CONFIG_FILE="$GF_HOME/config.yaml"

if [ ! -f "$CONFIG_FILE" ]; then
  echo "error: config file not found at $CONFIG_FILE" >&2
  exit 1
fi

# Extract API token and listen address without logging token
TOKEN=$(awk '/^[[:space:]]*api_token:[[:space:]]*/ {print $2; exit}' "$CONFIG_FILE" | tr -d '"'\''')
LISTEN=$(awk '/^[[:space:]]*listen:[[:space:]]*/ {print $2; exit}' "$CONFIG_FILE" | tr -d '"'\''')

if [ -z "$TOKEN" ]; then
  echo "error: api_token not found in $CONFIG_FILE" >&2
  exit 1
fi

if [ -z "$LISTEN" ]; then
  LISTEN="127.0.0.1:7878"
fi

case "$LISTEN" in
  :*)
    BASE_URL="http://127.0.0.1$LISTEN"
    ;;
  http://*|https://*)
    BASE_URL="$LISTEN"
    ;;
  *)
    BASE_URL="http://$LISTEN"
    ;;
esac
BASE_URL="${BASE_URL%/}"

TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT

CURL_EXIT=0
if [ -n "$BODY" ]; then
  HTTP_CODE=$(curl -sS -w "%{http_code}" -o "$TMPFILE" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -X "$METHOD" \
    -d "$BODY" \
    "${BASE_URL}${ENDPOINT}") || CURL_EXIT=$?
else
  HTTP_CODE=$(curl -sS -w "%{http_code}" -o "$TMPFILE" \
    -H "Authorization: Bearer $TOKEN" \
    -X "$METHOD" \
    "${BASE_URL}${ENDPOINT}") || CURL_EXIT=$?
fi

if [ "$CURL_EXIT" -ne 0 ]; then
  exit "$CURL_EXIT"
fi

if [ "$HTTP_CODE" -ge 200 ] && [ "$HTTP_CODE" -le 299 ]; then
  cat "$TMPFILE"
  exit 0
else
  cat "$TMPFILE" >&2
  exit 1
fi
