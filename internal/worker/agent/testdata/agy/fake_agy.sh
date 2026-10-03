#!/bin/sh
# fake_agy.sh is a test double stub that simulates the agy CLI tool without needing a real agy binary.

# If requested, record command-line arguments to a destination file
if [ -n "$FAKE_AGY_RECORD_ARGS" ]; then
    printf "%s\n" "$@" > "$FAKE_AGY_RECORD_ARGS"
fi

# If requested, record environment variables to a destination file
if [ -n "$FAKE_AGY_RECORD_ENV" ]; then
    env > "$FAKE_AGY_RECORD_ENV"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

case "$FAKE_AGY_MODE" in
    "agent_failure")
        cat "$SCRIPT_DIR/agent_failure.stdout"
        exit 0
        ;;
    "failure")
        cat "$SCRIPT_DIR/failure.stdout"
        exit 0
        ;;
    "garbage")
        cat "$SCRIPT_DIR/garbage.stdout"
        exit 0
        ;;
    "crash")
        echo "agy fatal error: segmentation fault" >&2
        exit 2
        ;;
    "timeout")
        sleep 60
        exit 0
        ;;
    *)
        # Default mode: success
        cat "$SCRIPT_DIR/success.stdout"
        exit 0
        ;;
esac
