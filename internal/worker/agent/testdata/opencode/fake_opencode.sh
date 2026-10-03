#!/bin/sh
# fake_opencode.sh is a test double stub that simulates the opencode CLI tool.

if [ -n "$FAKE_OPENCODE_RECORD_ARGS" ]; then
    printf "%s\n" "$@" > "$FAKE_OPENCODE_RECORD_ARGS"
fi

if [ -n "$FAKE_OPENCODE_RECORD_ENV" ]; then
    env > "$FAKE_OPENCODE_RECORD_ENV"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

case "$FAKE_OPENCODE_MODE" in
    "truncated")
        cat "$SCRIPT_DIR/truncated.ndjson"
        exit 0
        ;;
    "error_event")
        cat "$SCRIPT_DIR/error_event.ndjson"
        exit 0
        ;;
    "crash")
        echo "opencode fatal error: syntax error" >&2
        exit 1
        ;;
    "timeout")
        sleep 60
        exit 0
        ;;
    *)
        # Default: success
        cat "$SCRIPT_DIR/success.ndjson"
        exit 0
        ;;
esac
