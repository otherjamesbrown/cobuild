#!/bin/bash
# CoBuild session event hook — tracks file reads, detects waste, records to DB.
#
# Called by Claude Code hooks on: SessionStart, SessionEnd, PreToolUse,
# PostToolUse, PreCompact, PostCompact, Stop, StopFailure.
#
# Only tracks CoBuild-dispatched sessions (COBUILD_DISPATCH=true).

set -euo pipefail

# Only track CoBuild-dispatched sessions
if [ "${COBUILD_DISPATCH:-}" != "true" ]; then
    exit 0
fi

# Read hook JSON from stdin
INPUT=$(cat)

EVENT=$(echo "$INPUT" | jq -r '.hook_event_name // "unknown"')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // "unknown"')
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
CWD=$(echo "$INPUT" | jq -r '.cwd // ""')

# Session state directory — persists across hook calls within a session
STATE_DIR="${CWD:-.}/.cobuild/session-state"
mkdir -p "$STATE_DIR"
READS_FILE="$STATE_DIR/files_read.json"
EVENTS_FILE="${CWD:-.}/.cobuild/events.jsonl"

# Extract event-specific fields
TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null || echo "")
TOOL_INPUT=$(echo "$INPUT" | jq -c '.tool_input // empty' 2>/dev/null || echo "")

case "$EVENT" in
    SessionStart)
        # Initialize session state
        echo '{}' > "$READS_FILE"
        jq -n -c --arg ts "$TIMESTAMP" --arg sid "$SESSION_ID" \
            '{ts: $ts, event: "session_start", session_id: $sid}' >> "$EVENTS_FILE"
        ;;

    PreToolUse)
        case "$TOOL_NAME" in
            Read)
                FILE_PATH=$(echo "$TOOL_INPUT" | jq -r '.file_path // empty' 2>/dev/null || echo "")
                if [ -n "$FILE_PATH" ]; then
                    # Estimate tokens from file size
                    TOKENS=0
                    if [ -f "$FILE_PATH" ]; then
                        CHARS=$(wc -c < "$FILE_PATH" 2>/dev/null || echo 0)
                        TOKENS=$((CHARS / 4))
                    fi

                    # Check if already read this session
                    PREV_COUNT=0
                    if [ -f "$READS_FILE" ]; then
                        PREV_COUNT=$(jq -r --arg f "$FILE_PATH" '.[$f] // 0' "$READS_FILE" 2>/dev/null || echo 0)
                    fi

                    if [ "$PREV_COUNT" -gt 0 ]; then
                        # REPEATED READ — warn via stderr (shows in Claude's context)
                        BASENAME=$(basename "$FILE_PATH")
                        echo "⚡ CoBuild: $BASENAME was already read this session (~${TOKENS} tokens). Use your existing knowledge of this file." >&2

                        jq -n -c --arg ts "$TIMESTAMP" --arg f "$FILE_PATH" --argjson t "$TOKENS" --argjson c "$((PREV_COUNT + 1))" \
                            '{ts: $ts, event: "repeated_read", file: $f, tokens_saved: $t, read_count: $c}' >> "$EVENTS_FILE"
                    else
                        jq -n -c --arg ts "$TIMESTAMP" --arg f "$FILE_PATH" --argjson t "$TOKENS" \
                            '{ts: $ts, event: "file_read", file: $f, tokens_estimated: $t}' >> "$EVENTS_FILE"
                    fi

                    # Update read count
                    if [ -f "$READS_FILE" ]; then
                        jq --arg f "$FILE_PATH" '.[$f] = ((.[$f] // 0) + 1)' "$READS_FILE" > "$READS_FILE.tmp" && mv "$READS_FILE.tmp" "$READS_FILE"
                    fi
                fi
                ;;

            Edit|Write)
                FILE_PATH=$(echo "$TOOL_INPUT" | jq -r '.file_path // empty' 2>/dev/null || echo "")
                TOKENS=0
                if [ -n "$FILE_PATH" ] && [ -f "$FILE_PATH" ]; then
                    CHARS=$(wc -c < "$FILE_PATH" 2>/dev/null || echo 0)
                    TOKENS=$((CHARS / 4))
                fi
                jq -n -c --arg ts "$TIMESTAMP" --arg f "$FILE_PATH" --argjson t "$TOKENS" --arg tool "$TOOL_NAME" \
                    '{ts: $ts, event: "file_write", file: $f, tokens_estimated: $t, tool: $tool}' >> "$EVENTS_FILE"
                ;;

            Bash)
                COMMAND=$(echo "$TOOL_INPUT" | jq -r '.command // empty' 2>/dev/null || echo "")
                jq -n -c --arg ts "$TIMESTAMP" --arg cmd "$COMMAND" \
                    '{ts: $ts, event: "bash_run", command: ($cmd | .[0:200])}' >> "$EVENTS_FILE"
                ;;

            Grep|Glob)
                jq -n -c --arg ts "$TIMESTAMP" --arg tool "$TOOL_NAME" \
                    '{ts: $ts, event: "search", tool: $tool}' >> "$EVENTS_FILE"
                ;;

            Agent)
                jq -n -c --arg ts "$TIMESTAMP" \
                    '{ts: $ts, event: "subagent_spawn"}' >> "$EVENTS_FILE"
                ;;
        esac
        ;;

    PreCompact)
        jq -n -c --arg ts "$TIMESTAMP" \
            '{ts: $ts, event: "compact_start"}' >> "$EVENTS_FILE"
        ;;

    PostCompact)
        jq -n -c --arg ts "$TIMESTAMP" \
            '{ts: $ts, event: "compact_end"}' >> "$EVENTS_FILE"
        ;;

    Stop)
        jq -n -c --arg ts "$TIMESTAMP" \
            '{ts: $ts, event: "turn_complete"}' >> "$EVENTS_FILE"
        ;;

    StopFailure)
        ERROR=$(echo "$INPUT" | jq -r '.error // "unknown"')
        jq -n -c --arg ts "$TIMESTAMP" --arg err "$ERROR" \
            '{ts: $ts, event: "turn_error", error: $err}' >> "$EVENTS_FILE"
        ;;

    SessionEnd)
        # Generate session summary from events
        if [ -f "$EVENTS_FILE" ] && command -v jq &>/dev/null; then
            TOTAL_READS=$(jq -s '[.[] | select(.event == "file_read")] | length' "$EVENTS_FILE" 2>/dev/null || echo 0)
            REPEATED_READS=$(jq -s '[.[] | select(.event == "repeated_read")] | length' "$EVENTS_FILE" 2>/dev/null || echo 0)
            TOKENS_SAVED=$(jq -s '[.[] | select(.event == "repeated_read") | .tokens_saved] | add // 0' "$EVENTS_FILE" 2>/dev/null || echo 0)
            TOTAL_WRITES=$(jq -s '[.[] | select(.event == "file_write")] | length' "$EVENTS_FILE" 2>/dev/null || echo 0)
            COMPACTIONS=$(jq -s '[.[] | select(.event == "compact_start")] | length' "$EVENTS_FILE" 2>/dev/null || echo 0)
            ERRORS=$(jq -s '[.[] | select(.event == "turn_error")] | length' "$EVENTS_FILE" 2>/dev/null || echo 0)

            jq -n -c --arg ts "$TIMESTAMP" --arg sid "$SESSION_ID" \
                --argjson reads "$TOTAL_READS" --argjson repeats "$REPEATED_READS" \
                --argjson saved "$TOKENS_SAVED" --argjson writes "$TOTAL_WRITES" \
                --argjson compacts "$COMPACTIONS" --argjson errors "$ERRORS" \
                '{ts: $ts, event: "session_end", session_id: $sid, summary: {reads: $reads, repeated_reads: $repeats, tokens_saved_by_repeat_detection: $saved, writes: $writes, compactions: $compacts, errors: $errors}}' >> "$EVENTS_FILE"
        fi

        # Write to DB if available
        PIPELINE_SESSION_ID="${COBUILD_SESSION_ID:-}"
        if [ -n "$PIPELINE_SESSION_ID" ]; then
            DSN=""
            HOST=$(grep "host:" "$HOME/.cobuild/config.yaml" 2>/dev/null | awk '{print $2}' | head -1)
            DB=$(grep "database:" "$HOME/.cobuild/config.yaml" 2>/dev/null | awk '{print $2}' | head -1)
            USER=$(grep "user:" "$HOME/.cobuild/config.yaml" 2>/dev/null | awk '{print $2}' | head -1)
            SSLMODE=$(grep "sslmode:" "$HOME/.cobuild/config.yaml" 2>/dev/null | awk '{print $2}' | head -1)
            if [ -n "$HOST" ] && [ -n "$DB" ] && [ -n "$USER" ]; then
                DSN="host=$HOST dbname=$DB user=$USER sslmode=${SSLMODE:-verify-full}"
                psql "$DSN" -c "INSERT INTO pipeline_session_events (id, session_id, event_type, detail, tokens_used, timestamp) VALUES (
                    'pse-$(openssl rand -hex 4)',
                    '$PIPELINE_SESSION_ID',
                    'session_summary',
                    'Reads: ${TOTAL_READS}, Repeated: ${REPEATED_READS}, Tokens saved: ${TOKENS_SAVED}, Writes: ${TOTAL_WRITES}, Compactions: ${COMPACTIONS}',
                    $TOKENS_SAVED,
                    '$TIMESTAMP'
                )" 2>/dev/null || true
            fi
        fi
        ;;
esac

exit 0
