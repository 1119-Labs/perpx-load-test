#!/bin/bash
# ============================================================================
# PerpX Load Test - Automated Load Test Benchmark Script
# ============================================================================
# Automates the process of seeding accounts and running TPS benchmarks.
#
# Usage:
#   ./scripts/run_benchmark.sh [OPTIONS]
#
# Options:
#   --workers N            Number of workers (default: 10)
#   --duration N           Duration in seconds (default: 30)
#   --rate N               Transactions per second per worker (default: 50)
#   --skip-seed            Skip account seeding (assumes already seeded)
#   --skip-build           Skip building the binary (assumes already built)
#   --output-dir DIR       Output directory for results (default: ./exported)
#   --help                 Show this help message
# ============================================================================

set -eo pipefail

# ============================================================================
# Configuration
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Repository root is the parent of the scripts directory
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
# Build directory - use repo root/bin or build subdirectory
BUILD_DIR="${BUILD_DIR:-$REPO_ROOT/bin}"
BINARY="$BUILD_DIR/perpx-load-test"

# Default values
WORKERS="${WORKERS:-10}"
DURATION="${DURATION:-30}"
RATE="${RATE:-50}"
SKIP_SEED="${SKIP_SEED:-false}"
SKIP_BUILD="${SKIP_BUILD:-false}"
OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/exported}"
SEED_PRIVATE_KEY="${SEED_PRIVATE_KEY:-}"
SEED_KEY="${SEED_KEY:-alice}"
QUIET="${QUIET:-true}"
SHOW_LIVE_LOGS="${SHOW_LIVE_LOGS:-false}"
UI_MODE="${UI_MODE:-plain}"

# Default configuration (can be overridden via environment variables)
CHAIN_ID="${CHAIN_ID:-localperpxprotocol}"
RPC_URL="${RPC_URL:-http://localhost:36657}"
# Primary WebSocket endpoint (single RPC)
WS_URL="${WS_URL:-ws://localhost:36657/websocket}"
# Optional: comma-separated list of WebSocket endpoints for multiple RPCs.
# If not provided, defaults to WS_URL so existing behavior is preserved.
WS_ENDPOINTS="${WS_ENDPOINTS:-$WS_URL}"

# Colors for output
COLOR_RESET='\033[0m'
COLOR_INFO='\033[0;34m'
COLOR_SUCCESS='\033[0;32m'
COLOR_WARNING='\033[0;33m'
COLOR_ERROR='\033[0;31m'
COLOR_BOLD='\033[1m'

# ============================================================================
# Helper Functions
# ============================================================================

log_info() {
	echo -e "${COLOR_INFO}[INFO]${COLOR_RESET} $*" >&2
}

log_success() {
	echo -e "${COLOR_SUCCESS}[✓]${COLOR_RESET} $*" >&2
}

log_error() {
	echo -e "${COLOR_ERROR}[ERROR]${COLOR_RESET} $*" >&2
}

log_warning() {
	echo -e "${COLOR_WARNING}[WARN]${COLOR_RESET} $*" >&2
}

ws_to_http_base() {
	# Convert ws://host:port/websocket (or wss://) to http(s)://host:port
	# and strip any trailing /websocket path.
	local ws="$1"
	local http="$ws"
	http="${http#ws://}"
	http="http://${http#http://}"
	if [[ "$ws" == wss://* ]]; then
		http="${ws#wss://}"
		http="https://${http#https://}"
	fi
	# Strip trailing /websocket if present
	http="${http%/websocket}"
	echo "$http"
}

validate_ws_endpoints_reachable() {
	# Validate each ws endpoint by querying the corresponding HTTP RPC /status.
	# This catches the common case where one of the provided endpoints isn't running,
	# which otherwise causes perpx-load-test to exit with code 1 and little/no output in TUI mode.
	local raw="$1"
	local -a parts endpoints
	local ep http_base
	local failed=0

	IFS=',' read -ra parts <<<"$raw"
	endpoints=()
	for ep in "${parts[@]}"; do
		ep="${ep//[[:space:]]/}"
		if [ -n "$ep" ]; then
			endpoints+=("$ep")
		fi
	done

	if [ "${#endpoints[@]}" -eq 0 ]; then
		log_error "No WebSocket endpoints provided"
		return 1
	fi

	log_info "Validating WebSocket endpoints..."
	for ep in "${endpoints[@]}"; do
		http_base="$(ws_to_http_base "$ep")"
		# Try /status on the corresponding HTTP RPC endpoint.
		if ! curl -fsS "${http_base}/status" >/dev/null 2>&1; then
			log_error "Endpoint not reachable: $ep (RPC status check failed at ${http_base}/status)"
			failed=1
		else
			log_success "Endpoint OK: $ep"
		fi
	done

	if [ "$failed" -ne 0 ]; then
		log_error "One or more endpoints are not reachable."
		log_error "Fix: start the missing node(s), or remove them from WS_ENDPOINTS/--ws-endpoints."
		return 1
	fi
	return 0
}

count_ws_endpoints() {
	# Counts non-empty comma-separated endpoints in $1, stripping whitespace.
	local raw="$1"
	local -a parts endpoints
	local ep
	IFS=',' read -ra parts <<<"$raw"
	endpoints=()
	for ep in "${parts[@]}"; do
		# trim all whitespace characters
		ep="${ep//[[:space:]]/}"
		if [ -n "$ep" ]; then
			endpoints+=("$ep")
		fi
	done
	echo "${#endpoints[@]}"
}

show_help() {
	cat <<EOF
PerpX Load Test - Automated Load Test Benchmark

Usage: $0 [OPTIONS]

Options:
  --workers N            Number of workers (default: 10)
  --duration N           Duration in seconds (default: 30)
  --rate N               Transactions per second per worker (default: 50)
  --skip-seed            Skip account seeding (assumes already seeded)
  --skip-build           Skip building the binary (assumes already built)
  --output-dir DIR       Output directory for results (default: ./exported)
  --seed-private-key KEY  Hex-encoded private key to use for seeding (takes precedence over --seed-key)
  --seed-key KEY          Key name or mnemonic to use for seeding (default: alice)
  --ws-endpoints LIST     Comma-separated list of WebSocket endpoints for load test (overrides WS_URL for load test only)
  --quiet                Quiet UI (default: on) - shows a simple progress line and writes full logs to a file
  --verbose-logs         Stream full loadtest logs to the terminal (disables quiet UI)
  --tui                  Full-screen realtime TUI (htop-like). Implies: --ui tui (no quiet/progress wrapper)
  --help                 Show this help message

Environment Variables:
  WORKERS                Override number of workers
  DURATION               Override duration in seconds
  RATE                   Override transaction rate per worker
  SKIP_SEED              Set to 'true' to skip seeding
  SKIP_BUILD             Set to 'true' to skip building
  OUTPUT_DIR             Override output directory
  SEED_PRIVATE_KEY       Hex-encoded private key for seeding (takes precedence over SEED_KEY)
  SEED_KEY               Key name or mnemonic for seeding (default: alice)
  CHAIN_ID               Override chain ID (default: localperpxprotocol)
  RPC_URL                Override RPC endpoint (default: http://localhost:36657)
  WS_URL                 Override default WebSocket endpoint (default: ws://localhost:36657/websocket)
  WS_ENDPOINTS           Comma-separated list of WebSocket endpoints for load test
  QUIET                  Set to 'true' for quiet UI (default: true)
  SHOW_LIVE_LOGS         Set to 'true' to stream full logs to terminal (default: false)
  UI_MODE                UI mode for perpx-load-test: plain or tui (default: plain)

Examples:
  # Basic benchmark (10 workers, 30 seconds, 50 TPS per worker)
  $0

  # Higher load test
  $0 --workers 20 --duration 60 --rate 100

  # Skip seeding (accounts already funded)
  $0 --skip-seed

  # Use private key for seeding
  $0 --seed-private-key "6cf5103c60c939a5f38e383b52239c5296c968579eec1c68a47d70fbf1d19159"

  # Custom output directory
  $0 --output-dir ./results
EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
	case "$1" in
		--workers|-w)
			WORKERS="$2"
			shift 2
			;;
		--duration|-d)
			DURATION="$2"
			shift 2
			;;
		--rate|-r)
			RATE="$2"
			shift 2
			;;
		--skip-seed)
			SKIP_SEED="true"
			shift
			;;
		--skip-build)
			SKIP_BUILD="true"
			shift
			;;
		--output-dir|-o)
			OUTPUT_DIR="$2"
			shift 2
			;;
		--seed-private-key|--private-key|-p)
			SEED_PRIVATE_KEY="$2"
			shift 2
			;;
		--seed-key|-k)
			SEED_KEY="$2"
			shift 2
			;;
		--ws-endpoints)
			WS_ENDPOINTS="$2"
			shift 2
			;;
		--quiet)
			QUIET="true"
			shift
			;;
		--verbose-logs)
			QUIET="false"
			SHOW_LIVE_LOGS="true"
			shift
			;;
		--tui)
			UI_MODE="tui"
			# TUI needs a clean stdout (no wrappers/pipes), so disable other UIs/log streaming.
			QUIET="false"
			SHOW_LIVE_LOGS="false"
			shift
			;;
		--help|-h)
			show_help
			exit 0
			;;
		*)
			log_error "Unknown option: $1"
			show_help
			exit 1
			;;
	esac
done

# ============================================================================
# Validation
# ============================================================================

# Check if required commands exist
for cmd in curl jq go; do
	if ! command -v "$cmd" &> /dev/null; then
		log_error "Required command '$cmd' not found. Please install it."
		exit 1
	fi
done

# Detect a timeout command (GNU coreutils `timeout` or Homebrew `gtimeout` on macOS).
# If none is available, we will run benchmarks without an external timeout wrapper.
TIMEOUT_CMD=""
if command -v timeout >/dev/null 2>&1; then
	TIMEOUT_CMD="timeout"
elif command -v gtimeout >/dev/null 2>&1; then
	TIMEOUT_CMD="gtimeout"
else
	log_warning "No 'timeout' or 'gtimeout' command found; benchmarks will run without an automatic timeout."
	log_warning "On macOS you can install it via: brew install coreutils (then 'gtimeout' will be available)."
fi

# Find system Go binary
SYSTEM_GO=$(command -v go)
if [ -z "$SYSTEM_GO" ] || [ ! -x "$SYSTEM_GO" ]; then
	# Try common locations
	if [ -x "/usr/local/go/bin/go" ]; then
		SYSTEM_GO="/usr/local/go/bin/go"
	elif [ -x "$HOME/go/bin/go" ]; then
		SYSTEM_GO="$HOME/go/bin/go"
	else
		log_error "Cannot find Go binary. Please install Go."
		exit 1
	fi
fi

# Check Go version compatibility
log_info "Checking Go version..."
GO_VERSION=$("$SYSTEM_GO" version 2>/dev/null | awk '{print $3}' | sed 's/go//' || echo "")
if [ -z "$GO_VERSION" ]; then
	log_error "Failed to determine Go version from $SYSTEM_GO"
	exit 1
fi
log_info "Go version: $GO_VERSION (using $SYSTEM_GO)"

# Check for Go version mismatch (common issue)
if go version -m "$(which go)" 2>/dev/null | grep -q "version mismatch"; then
	log_warning "Go version mismatch detected. This may cause build failures."
	log_warning "Try running: go clean -cache && go clean -modcache"
	log_warning "Or reinstall Go to fix version mismatch."
fi

# Check if localnet is running
log_info "Checking if localnet is running..."
if ! curl -s "$RPC_URL/status" > /dev/null 2>&1; then
	log_error "Cannot connect to RPC endpoint: $RPC_URL"
	log_error "Please ensure the localnet is running (make start)."
	exit 1
fi
log_success "Localnet is running"

# Validate all WebSocket endpoints are reachable before we seed + run load test.
if ! validate_ws_endpoints_reachable "$WS_ENDPOINTS"; then
	exit 1
fi

# Get chain info
CHAIN_STATUS=$(curl -s "$RPC_URL/status")
CURRENT_HEIGHT=$(echo "$CHAIN_STATUS" | jq -r '.result.sync_info.latest_block_height // empty')
if [ -z "$CURRENT_HEIGHT" ] || [ "$CURRENT_HEIGHT" = "null" ]; then
	log_error "Failed to get chain status. Is the chain running?"
	exit 1
fi
log_info "Current block height: $CURRENT_HEIGHT"

# Determine endpoint count for correct seeding + reporting.
ENDPOINT_COUNT="$(count_ws_endpoints "$WS_ENDPOINTS")"
if [ -z "$ENDPOINT_COUNT" ] || [ "$ENDPOINT_COUNT" -lt 1 ]; then
	ENDPOINT_COUNT=1
fi

# ============================================================================
# Build Binary
# ============================================================================

if [ "$SKIP_BUILD" != "true" ]; then
	log_info "Building perpx-load-test binary..."
	cd "$REPO_ROOT"
	
	# Create build directory if it doesn't exist
	mkdir -p "$BUILD_DIR"
	
	# Build the binary with error capture
	BUILD_LOG=$(mktemp)
	log_info "Running: $SYSTEM_GO build -o $BINARY ./cmd/perpx-load-test"
	
	# Attempt build using system Go
	if ! "$SYSTEM_GO" build -o "$BINARY" ./cmd/perpx-load-test 2>&1 | tee "$BUILD_LOG"; then
		log_error "Failed to build binary"
		
		# Check for common Go version mismatch error
		if grep -q "version.*does not match go tool version" "$BUILD_LOG" 2>/dev/null; then
			log_error ""
			log_error "Go version mismatch detected!"
			log_error "The Go compiler version doesn't match the Go tool version."
			log_error ""
			log_warning "Attempting automatic fix..."
			
			# Try aggressive cache cleaning
			log_info "Cleaning Go build cache..."
			# Use the system go command directly (from PATH)
			SYSTEM_GO=$(command -v go)
			if [ -z "$SYSTEM_GO" ] || [ ! -x "$SYSTEM_GO" ]; then
				log_error "Cannot find system go command"
				rm -f "$BUILD_LOG"
				exit 1
			fi
			
			# Clean caches using system go
			"$SYSTEM_GO" clean -cache 2>/dev/null || true
			"$SYSTEM_GO" clean -testcache 2>/dev/null || true
			# Don't clean modcache as it will require re-downloading everything
			# "$SYSTEM_GO" clean -modcache 2>/dev/null || true
			
			# Try to rebuild standard library by forcing a rebuild
			log_info "Forcing rebuild of standard library packages..."
			# Unset GOROOT to use system Go
			unset GOROOT
			"$SYSTEM_GO" install -a std 2>/dev/null || true
			
			# Try build again using system go
			log_info "Retrying build after cache clean..."
			if "$SYSTEM_GO" build -o "$BINARY" ./cmd/perpx-load-test 2>&1 | tee "$BUILD_LOG"; then
				log_success "Build succeeded after cache clean!"
				rm -f "$BUILD_LOG"
			else
				log_error ""
				log_error "Automatic fix failed. Manual intervention required:"
				log_error "  1. Clean all Go caches:"
				log_error "     go clean -cache -modcache -testcache"
				log_error "  2. Rebuild standard library:"
				log_error "     go install -a std"
				log_error "  3. Or reinstall Go to ensure versions match:"
				log_error "     - Remove /usr/local/go"
				log_error "     - Download and install Go 1.25.4 from https://go.dev/dl/"
				log_error "  4. Or use --skip-build if you have a pre-built binary"
				log_error ""
				# Try to get Go version safely
				if SYSTEM_GO=$(command -v go) && [ -x "$SYSTEM_GO" ]; then
					log_error "Current Go version: $($SYSTEM_GO version 2>/dev/null || echo 'unknown')"
					log_error "GOROOT: $($SYSTEM_GO env GOROOT 2>/dev/null || echo 'unknown')"
				else
					log_error "Go command not available"
				fi
				rm -f "$BUILD_LOG"
				exit 1
			fi
		else
			# Other build error
			log_error "Build failed. See output above for details."
			rm -f "$BUILD_LOG"
			exit 1
		fi
	fi
	rm -f "$BUILD_LOG"
	
	if [ ! -f "$BINARY" ]; then
		log_error "Failed to build binary at $BINARY"
		exit 1
	fi
	log_success "Binary built successfully at $BINARY"
else
	log_info "Skipping build (using existing binary)"
	if [ ! -f "$BINARY" ]; then
		log_error "Binary not found at $BINARY. Run without --skip-build first."
		log_info "You can build it manually with: go build -o $BINARY ./cmd/perpx-load-test"
		exit 1
	fi
fi

# ============================================================================
# Output Files (create early so seeding + benchmark share the same timestamp/logs)
# ============================================================================

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Generate timestamp for unique output file
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_FILE="$OUTPUT_DIR/loadtest-stats-${TIMESTAMP}.csv"
JSON_OUTPUT="$OUTPUT_DIR/loadtest-results-${TIMESTAMP}.json"
LOG_FILE="$OUTPUT_DIR/loadtest-run-${TIMESTAMP}.log"
SEED_LOG_FILE="$OUTPUT_DIR/loadtest-seed-${TIMESTAMP}.log"

# ============================================================================
# Seed Accounts
# ============================================================================

if [ "$SKIP_SEED" != "true" ]; then
	log_info "Seeding benchmark accounts..."
	# NOTE: perpx-load-test uses -c as "connections per endpoint". With multiple endpoints,
	# the total number of distinct accounts needed is WORKERS * ENDPOINT_COUNT.
	SEED_WORKERS=$((WORKERS * ENDPOINT_COUNT))
	log_info "  Connections per endpoint (-c): $WORKERS"
	log_info "  Endpoints: $ENDPOINT_COUNT"
	log_info "  Accounts to seed: $SEED_WORKERS"
	log_info "  Chain ID: $CHAIN_ID"
	log_info "  RPC: $RPC_URL"
	
	# Build seed command arguments
	SEED_ARGS=(
		--workers "$SEED_WORKERS"
		--rpc "$RPC_URL"
		--chain-id "$CHAIN_ID"
	)
	
	# Add private key if provided (takes precedence)
	if [ -n "$SEED_PRIVATE_KEY" ]; then
		log_info "  Using private key for seeding"
		SEED_ARGS+=(--seed-private-key "$SEED_PRIVATE_KEY")
	else
		log_info "  Using seed key: $SEED_KEY"
		SEED_ARGS+=(--seed-key "$SEED_KEY")
	fi
	
	if [ "$UI_MODE" = "tui" ]; then
		log_info "TUI mode: writing seeding logs to $SEED_LOG_FILE"
		if ! "$BINARY" seed "${SEED_ARGS[@]}" >"$SEED_LOG_FILE" 2>&1; then
			log_error "Account seeding failed (last 30 lines):"
			tail -30 "$SEED_LOG_FILE" 2>/dev/null || true
			exit 1
		fi
	elif ! "$BINARY" seed "${SEED_ARGS[@]}"; then
		log_error "Account seeding failed"
		exit 1
	fi
	log_success "Accounts seeded successfully"
else
	log_info "Skipping account seeding"
fi

# ============================================================================
# Run Benchmark
# ============================================================================

log_info "Starting TPS benchmark..."
log_info "  Connections per endpoint (-c): $WORKERS"
log_info "  Endpoints: $ENDPOINT_COUNT"
log_info "  Duration: ${DURATION}s"
log_info "  Rate: $RATE TPS per connection (total attempted: $((WORKERS * RATE * ENDPOINT_COUNT)) TPS)"
log_info "  Chain ID: $CHAIN_ID"
log_info "  WebSocket endpoints: $WS_ENDPOINTS"

# Calculate total expected transactions (attempted)
TOTAL_EXPECTED=$((WORKERS * RATE * ENDPOINT_COUNT * DURATION))

log_warning "Note: If you see 'connection reset' errors, increase ulimit: ulimit -n 4096"
echo ""

# Run the benchmark
START_TIME=$(date +%s)
START_HEIGHT=$(curl -s "$RPC_URL/status" | jq -r '.result.sync_info.latest_block_height')

# Export environment variables for the loadtest client
export LOADTEST_CHAIN_ID="$CHAIN_ID"
export LOADTEST_DENOM="${LOADTEST_DENOM:-aperpx}"

# Run the benchmark and capture output
log_info "Running benchmark command..."
log_info "  Command: $BINARY --ui $UI_MODE -c $WORKERS -T $DURATION -r $RATE -s 250 --broadcast-tx-method async --endpoints $WS_ENDPOINTS --stats-output $OUTPUT_FILE"
log_info "  Logs: $LOG_FILE"

# Run with timeout to prevent infinite hanging (if timeout command is available)
TIMEOUT_DURATION=$((DURATION + 60))

if [ "$UI_MODE" = "tui" ]; then
	# Full-screen TUI: run in foreground with stdout attached to terminal.
	# Save stderr to a log file for debugging.
	log_info "TUI mode enabled (full-screen realtime view)"
	log_info "Press Ctrl+C to stop the test early"
	echo ""
	
	# Ensure we have a TTY for TUI to work properly
	if [ ! -t 1 ]; then
		log_warning "Warning: stdout is not a terminal. TUI may not display correctly."
		log_warning "Consider running without --tui or ensure you're in an interactive terminal."
	fi
	
	# Run TUI in foreground - it will take over the terminal.
	# If a timeout command is available, use it with --foreground so the TUI keeps control of the TTY.
	# Without a timeout command, run directly (no automatic timeout).
	# The TUI writes to stdout, so we don't redirect it; only redirect stderr to a log file.
	set +e  # Don't exit on error, we'll check exit code manually
	if [ -n "$TIMEOUT_CMD" ]; then
		"$TIMEOUT_CMD" --foreground "$TIMEOUT_DURATION" "$BINARY" \
			--ui tui \
			-c "$WORKERS" \
			-T "$DURATION" \
			-r "$RATE" \
			-s 250 \
			--broadcast-tx-method async \
			--endpoints "$WS_ENDPOINTS" \
			--stats-output "$OUTPUT_FILE" 2>"$LOG_FILE"
		EXIT_CODE=$?
	else
		"$BINARY" \
			--ui tui \
			-c "$WORKERS" \
			-T "$DURATION" \
			-r "$RATE" \
			-s 250 \
			--broadcast-tx-method async \
			--endpoints "$WS_ENDPOINTS" \
			--stats-output "$OUTPUT_FILE" 2>"$LOG_FILE"
		EXIT_CODE=$?
	fi
	set -e  # Re-enable exit on error
	
	# Clear any remaining TUI output and restore cursor
	# This ensures the terminal is in a good state after TUI exits
	echo ""
	tput cnorm 2>/dev/null || echo -e "\033[?25h"  # Show cursor
	tput sgr0 2>/dev/null || echo -e "\033[0m"     # Reset colors
elif [ "$QUIET" = "true" ] && [ "$SHOW_LIVE_LOGS" != "true" ]; then
	# Quiet UI: show a single progress line, write full logs to a file.
	# We also keep the last few lines on failure for quick debugging.
	log_info "Quiet mode enabled (set --verbose-logs to stream full logs)"

	# Start benchmark in background and capture output to log file.
	if [ -n "$TIMEOUT_CMD" ]; then
		( "$TIMEOUT_CMD" "$TIMEOUT_DURATION" "$BINARY" \
			--ui "$UI_MODE" \
			-c "$WORKERS" \
			-T "$DURATION" \
			-r "$RATE" \
			-s 250 \
			--broadcast-tx-method async \
			--endpoints "$WS_ENDPOINTS" \
			--stats-output "$OUTPUT_FILE" ) >"$LOG_FILE" 2>&1 &
	else
		( "$BINARY" \
			--ui "$UI_MODE" \
			-c "$WORKERS" \
			-T "$DURATION" \
			-r "$RATE" \
			-s 250 \
			--broadcast-tx-method async \
			--endpoints "$WS_ENDPOINTS" \
			--stats-output "$OUTPUT_FILE" ) >"$LOG_FILE" 2>&1 &
	fi
	BENCH_PID=$!

	# Simple progress loop (updates every second).
	for ((i=0; i<"$TIMEOUT_DURATION"; i++)); do
		if ! kill -0 "$BENCH_PID" 2>/dev/null; then
			break
		fi
		printf "\r%s[RUN]%s %ds/%ds  workers=%s  rate=%s  endpoints=%s" \
			"$COLOR_INFO" "$COLOR_RESET" "$i" "$DURATION" "$WORKERS" "$RATE" "$WS_ENDPOINTS"
		sleep 1
	done
	echo ""

	wait "$BENCH_PID"
	EXIT_CODE=$?
else
	# Verbose logs: stream everything to terminal and also save to log file.
	# `tee` preserves the exit code of the load test via PIPESTATUS[0].
	if [ -n "$TIMEOUT_CMD" ]; then
		"$TIMEOUT_CMD" "$TIMEOUT_DURATION" "$BINARY" \
			--ui "$UI_MODE" \
			-c "$WORKERS" \
			-T "$DURATION" \
			-r "$RATE" \
			-s 250 \
			--broadcast-tx-method async \
			--endpoints "$WS_ENDPOINTS" \
			--stats-output "$OUTPUT_FILE" 2>&1 | tee "$LOG_FILE"
		EXIT_CODE=${PIPESTATUS[0]}
	else
		"$BINARY" \
			--ui "$UI_MODE" \
			-c "$WORKERS" \
			-T "$DURATION" \
			-r "$RATE" \
			-s 250 \
			--broadcast-tx-method async \
			--endpoints "$WS_ENDPOINTS" \
			--stats-output "$OUTPUT_FILE" 2>&1 | tee "$LOG_FILE"
		EXIT_CODE=${PIPESTATUS[0]}
	fi
fi

# Check exit code
if [ -n "$TIMEOUT_CMD" ] && [ $EXIT_CODE -eq 124 ]; then
	log_error "Benchmark timed out after $TIMEOUT_DURATION seconds"
	if [ "$UI_MODE" = "tui" ]; then
		log_info "TUI may have been running but exceeded timeout. Check log file: $LOG_FILE"
	fi
	exit 1
elif [ $EXIT_CODE -ne 0 ]; then
	log_error "Benchmark failed with exit code $EXIT_CODE"
	if [ "$UI_MODE" = "tui" ]; then
		log_error "TUI mode was enabled. Check the log file for errors: $LOG_FILE"
	fi
	log_error "Last 30 log lines:"
	tail -30 "$LOG_FILE" 2>/dev/null || true
	exit 1
fi

# Check if stats file was created (indicates successful run)
if [ ! -f "$OUTPUT_FILE" ]; then
	log_warning "Stats file not created - benchmark may have failed silently"
fi

END_TIME=$(date +%s)
END_HEIGHT=$(curl -s "$RPC_URL/status" | jq -r '.result.sync_info.latest_block_height')
ELAPSED=$((END_TIME - START_TIME))

# ============================================================================
# Results Summary
# ============================================================================

echo ""
log_success "Benchmark Complete!"
echo ""
echo "=========================================="
echo "Benchmark Summary"
echo "=========================================="
echo "Configuration:"
echo "  Connections/EP:    $WORKERS"
echo "  Endpoints:         $ENDPOINT_COUNT"
echo "  Duration:          ${DURATION}s"
echo "  Rate per conn:     $RATE TPS"
echo "  Total attempted:   $((WORKERS * RATE * ENDPOINT_COUNT)) TPS"
echo ""
echo "Execution:"
echo "  Start height:      $START_HEIGHT"
echo "  End height:        $END_HEIGHT"
echo "  Blocks processed:  $((END_HEIGHT - START_HEIGHT))"
echo "  Elapsed time:      ${ELAPSED}s"
echo ""

# Parse CSV results if available and derive extra metrics
TOTAL_TIME="0"
TOTAL_TXS="0"
TOTAL_BYTES="0"
AVG_TX_RATE="0"
AVG_DATA_RATE="0"
AVG_TX_SIZE="0"
ATTEMPTED_TPS=$((WORKERS * RATE * ENDPOINT_COUNT))
ATTEMPTED_TXS=$((ATTEMPTED_TPS * DURATION))
BLOCKS_PROCESSED=$((END_HEIGHT - START_HEIGHT))
AVG_BLOCK_TIME="0"
TXS_PER_BLOCK="0"
EFFICIENCY="0"

if [ -f "$OUTPUT_FILE" ]; then
	TOTAL_TIME=$(grep "total_time" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	TOTAL_TXS=$(grep "total_txs" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	TOTAL_BYTES=$(grep "total_bytes" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	AVG_TX_RATE=$(grep "avg_tx_rate" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	AVG_DATA_RATE=$(grep "avg_data_rate" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	AVG_TX_SIZE=$(grep "avg_tx_size" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")

	if [ "$BLOCKS_PROCESSED" -gt 0 ]; then
		AVG_BLOCK_TIME=$(awk "BEGIN {printf \"%.3f\", $TOTAL_TIME / $BLOCKS_PROCESSED}")
		TXS_PER_BLOCK=$(awk "BEGIN {printf \"%.2f\", $TOTAL_TXS / $BLOCKS_PROCESSED}")
	fi

	if [ "$ATTEMPTED_TPS" -gt 0 ]; then
		EFFICIENCY=$(awk "BEGIN {printf \"%.2f\", ($AVG_TX_RATE * 100) / $ATTEMPTED_TPS}")
	fi

	log_info "Results from CSV:"
	echo "  total_time:        $TOTAL_TIME seconds"
	echo "  total_txs:         $TOTAL_TXS count"
	echo "  total_bytes:       $TOTAL_BYTES bytes"
	echo "  avg_tx_rate:       $AVG_TX_RATE transactions per second"
	echo "  avg_data_rate:     $AVG_DATA_RATE bytes per second"
	echo "  avg_tx_size:       $AVG_TX_SIZE bytes per transaction"
	echo "  actual_tps:        $AVG_TX_RATE (vs attempted $ATTEMPTED_TPS TPS, $EFFICIENCY% achieved)"
	echo "  avg_block_time:    ${AVG_BLOCK_TIME}s (over $BLOCKS_PROCESSED blocks)"
	echo "  txs_per_block:     $TXS_PER_BLOCK"
	echo ""
fi

# Create JSON summary
if command -v jq &> /dev/null && [ -f "$OUTPUT_FILE" ]; then
	log_info "Creating JSON summary..."
	
	# Print and persist a single-line result summary (easy to read/grep).
	RESULT_LINE="RESULT timestamp=${TIMESTAMP} avg_tps=${AVG_TX_RATE} attempted_tps=${ATTEMPTED_TPS} total_txs=${TOTAL_TXS} total_time_s=${TOTAL_TIME} workers=${WORKERS} rate_per_worker=${RATE} endpoints=${WS_ENDPOINTS} ui=${UI_MODE}"
	log_success "$RESULT_LINE"
	echo "$RESULT_LINE" >>"$LOG_FILE" 2>/dev/null || true
	
	jq -n \
		--arg chain_id "$CHAIN_ID" \
		--arg rpc_url "$RPC_URL" \
		--arg ws_url "$WS_URL" \
		--argjson workers "$WORKERS" \
		--argjson endpoints_count "$ENDPOINT_COUNT" \
		--argjson duration "$DURATION" \
		--argjson rate "$RATE" \
		--argjson start_height "$START_HEIGHT" \
		--argjson end_height "$END_HEIGHT" \
		--argjson blocks_processed $((END_HEIGHT - START_HEIGHT)) \
		--argjson elapsed_time "$ELAPSED" \
		--arg total_time "$TOTAL_TIME" \
		--arg total_txs "$TOTAL_TXS" \
		--arg total_bytes "$TOTAL_BYTES" \
		--arg avg_tx_rate "$AVG_TX_RATE" \
		--arg avg_data_rate "$AVG_DATA_RATE" \
		--arg avg_tx_size "$AVG_TX_SIZE" \
		--arg avg_block_time "$AVG_BLOCK_TIME" \
		--arg txs_per_block "$TXS_PER_BLOCK" \
		--arg attempted_tps "$ATTEMPTED_TPS" \
		--arg attempted_txs "$ATTEMPTED_TXS" \
		--arg efficiency_pct "$EFFICIENCY" \
		--arg timestamp "$TIMESTAMP" \
		'{
			timestamp: $timestamp,
			config: {
				chain_id: $chain_id,
				rpc_url: $rpc_url,
				ws_url: $ws_url,
				workers: $workers,
				endpoints_count: $endpoints_count,
				duration_seconds: $duration,
				rate_per_worker: $rate,
				total_attempted_tps: ($workers * $rate * $endpoints_count),
				total_attempted_txs: ($workers * $rate * $endpoints_count * $duration)
			},
			execution: {
				start_height: $start_height,
				end_height: $end_height,
				blocks_processed: $blocks_processed,
				elapsed_time_seconds: $elapsed_time
			},
			results: {
				total_time_seconds: ($total_time | tonumber),
				total_transactions: ($total_txs | tonumber),
				total_bytes: ($total_bytes | tonumber),
				average_tps: ($avg_tx_rate | tonumber),
				average_data_rate_bps: ($avg_data_rate | tonumber),
				average_tx_size_bytes: ($avg_tx_size | tonumber),
				average_block_time_seconds: ($avg_block_time | tonumber),
				txs_per_block: ($txs_per_block | tonumber),
				attempted_tps: ($attempted_tps | tonumber),
				attempted_txs: ($attempted_txs | tonumber),
				efficiency_percent: ($efficiency_pct | tonumber)
			}
		}' > "$JSON_OUTPUT"
	
	log_success "JSON summary saved to: $JSON_OUTPUT"
elif [ -f "$OUTPUT_FILE" ]; then
	# If jq isn't available, still print the single-line result from CSV.
	TOTAL_TIME=$(grep "total_time" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	TOTAL_TXS=$(grep "total_txs" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	AVG_TX_RATE=$(grep "avg_tx_rate" "$OUTPUT_FILE" | cut -d',' -f2 || echo "0")
	ATTEMPTED_TPS=$((WORKERS * RATE * ENDPOINT_COUNT))
	RESULT_LINE="RESULT timestamp=${TIMESTAMP} avg_tps=${AVG_TX_RATE} attempted_tps=${ATTEMPTED_TPS} total_txs=${TOTAL_TXS} total_time_s=${TOTAL_TIME} workers=${WORKERS} rate_per_worker=${RATE} endpoints=${WS_ENDPOINTS} ui=${UI_MODE}"
	log_success "$RESULT_LINE"
	echo "$RESULT_LINE" >>"$LOG_FILE" 2>/dev/null || true
else
	log_warning "No stats CSV found ($OUTPUT_FILE) - cannot summarize result"
fi

echo "=========================================="
echo ""
log_success "Results saved to:"
echo "  CSV: $OUTPUT_FILE"
[ -f "$JSON_OUTPUT" ] && echo "  JSON: $JSON_OUTPUT"
echo ""

