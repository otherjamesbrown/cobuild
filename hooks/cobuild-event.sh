#!/bin/bash
# CoBuild session event hook — records events to Postgres and local JSONL.
#
# Called by Claude Code hooks on: SessionStart, SessionEnd, PreToolUse,
# PostToolUse, PreCompact, PostCompact, Stop, StopFailure.
#
# Receives JSON on stdin with event data. Only records events for
# CoBuild-dispatched sessions (COBUILD_DISPATCH=true).

set -euo pipefail

# Only track CoBuild-dispatched sessions
if [ "${COBUILD_DISPATCH:-}" != "true" ]; then
    exit 0
fi

# Read hook JSON from stdin
INPUT=$(cat)

# Extract fields
EVENT=$(echo "$INPUT" | jq -r '.hook_event_name // "unknown"')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // "unknown"')
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Extract event-specific fields
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null || echo "")
TOOL_INPUT=$(echo "$INPUT" | jq -c '.tool_input // empty' 2>/dev/null || echo "")
FILE_PATH=""
COMMAND=""
DETAIL=""

# Classify event
case "$EVENT" in
    PreToolUse)
        case "$TOOL_NAME" in
            Read)
                EVENT_TYPE="file_read"
                FILE_PATH=$(echo "$TOOL_INPUT" | jq -r '.file_path // empty' 2>/dev/null || echo "")
                DETAIL="Read $FILE_PATH"
                ;;
            Edit|Write)
                EVENT_TYPE="file_edit"
                FILE_PATH=$(echo "$TOOL_INPUT" | jq -r '.file_path // empty' 2>/dev/null || echo "")
                DETAIL="Edit $FILE_PATH"
                ;;
            Bash)
                EVENT_TYPE="bash_run"
                COMMAND=$(echo "$TOOL_INPUT" | jq -r '.command // empty' 2>/dev/null || echo "")
                DETAIL=$(echo "$COMMAND" | head -1 | cut -c1-200)
                ;;
            Grep|Glob)
                EVENT_TYPE="search"
                DETAIL="$TOOL_NAME"
                ;;
            Agent)
                EVENT_TYPE="subagent"
                DETAIL="Spawn subagent"
                ;;
            *)
                EVENT_TYPE="tool_use"
                DETAIL="$TOOL_NAME"
                ;;
        esac
        ;;
    PostToolUse)
        EVENT_TYPE="tool_complete"
        DETAIL="$TOOL_NAME completed"
        ;;
    SessionStart)
        EVENT_TYPE="session_start"
        SOURCE=$(echo "$INPUT" | jq -r '.source // "unknown"')
        DETAIL="Session start ($SOURCE)"
        ;;
    SessionEnd)
        EVENT_TYPE="session_end"
        DETAIL="Session ended"
        ;;
    PreCompact)
        EVENT_TYPE="compact_start"
        DETAIL="Context compaction starting"
        ;;
    PostCompact)
        EVENT_TYPE="compact_end"
        DETAIL="Context compaction complete"
        ;;
    Stop)
        EVENT_TYPE="turn_complete"
        DETAIL="Agent turn completed"
        ;;
    StopFailure)
        EVENT_TYPE="turn_error"
        ERROR=$(echo "$INPUT" | jq -r '.error // "unknown"')
        DETAIL="Turn failed: $ERROR"
        ;;
    *)
        EVENT_TYPE="unknown"
        DETAIL="$EVENT"
        ;;
esac

# Write to local JSONL (fast, always works)
EVENTS_DIR="${COBUILD_EVENTS_DIR:-.cobuild}"
mkdir -p "$EVENTS_DIR"
EVENTS_FILE="$EVENTS_DIR/events.jsonl"

jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg et "$EVENT_TYPE" \
    --arg detail "$DETAIL" \
    --arg tool "$TOOL_NAME" \
    --arg file "$FILE_PATH" \
    --arg cmd "$COMMAND" \
    '{ts: $ts, session_id: $sid, event_type: $et, detail: $detail, tool: $tool, file: $file, command: $cmd}' \
    >> "$EVENTS_FILE"

# Write to Postgres if cobuild is available and session_id is tracked
# This uses the session_id stored in work item metadata by cobuild dispatch
if command -v cobuild &>/dev/null; then
    # Get the pipeline session ID from the environment or a marker file
    PIPELINE_SESSION_ID="${COBUILD_SESSION_ID:-}"
    if [ -z "$PIPELINE_SESSION_ID" ] && [ -f ".cobuild/session_id" ]; then
        PIPELINE_SESSION_ID=$(cat .cobuild/session_id)
    fi

    if [ -n "$PIPELINE_SESSION_ID" ]; then
        # Use psql directly for speed (hooks have 10s timeout)
        DSN="${COBUILD_DSN:-}"
        if [ -z "$DSN" ] && [ -f "$HOME/.cobuild/config.yaml" ]; then
            HOST=$(grep "host:" "$HOME/.cobuild/config.yaml" | awk '{print $2}' | head -1)
            DB=$(grep "database:" "$HOME/.cobuild/config.yaml" | awk '{print $2}' | head -1)
            USER=$(grep "user:" "$HOME/.cobuild/config.yaml" | awk '{print $2}' | head -1)
            SSLMODE=$(grep "sslmode:" "$HOME/.cobuild/config.yaml" | awk '{print $2}' | head -1)
            if [ -n "$HOST" ] && [ -n "$DB" ] && [ -n "$USER" ]; then
                DSN="host=$HOST dbname=$DB user=$USER sslmode=${SSLMODE:-verify-full}"
            fi
        fi

        if [ -n "$DSN" ]; then
            psql "$DSN" -c "INSERT INTO pipeline_session_events (id, session_id, event_type, detail, file_path, command, timestamp) VALUES ('pse-$(openssl rand -hex 4)', '$PIPELINE_SESSION_ID', '$EVENT_TYPE', '$(echo "$DETAIL" | sed "s/'/''/g")', '$(echo "$FILE_PATH" | sed "s/'/''/g")', '$(echo "$COMMAND" | head -1 | cut -c1-500 | sed "s/'/''/g")', '$TIMESTAMP')" 2>/dev/null || true
        fi
    fi
fi

exit 0
