#!/bin/bash
# ============================================================================
# PerpX Perps Load Runner - Order-Based Load-Test Scenarios (refactored)
# ============================================================================
set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="${BUILD_DIR:-$REPO_ROOT/bin}"
BINARY="$BUILD_DIR/perpx-load-test"

# Global configuration (overridable via env / CLI / YAML).
OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/exported}"
CONFIG_FILE="${CONFIG_FILE:-}"
STATS_OUTPUT_FILE="${STATS_OUTPUT_FILE:-}"
NO_STATS_OUTPUT="${NO_STATS_OUTPUT:-false}"

WORKERS="${WORKERS:-10}"
CONNECTIONS="${CONNECTIONS:-5}"
DURATION="${DURATION:-60}"
RATE="${RATE:-50}"
SCENARIO="${SCENARIO:-simple_perps}"
RNG_SEED="${RNG_SEED:-}"

CHAIN_ID="${LOADTEST_CHAIN_ID:-}"
RPC_URL="${LOADTEST_RPC:-}"
WS_URL="${WS_URL:-}"
REST_URL="${REST_URL:-}"

SKIP_SEED="${SKIP_SEED:-false}"
SKIP_BUILD="${SKIP_BUILD:-false}"
SEED_KEY="${SEED_KEY:-}"
SEED_PRIVATE_KEY="${SEED_PRIVATE_KEY:-}"

CHECK_TXS="${CHECK_TXS:-false}"
BROADCAST_TX_METHOD="${BROADCAST_TX_METHOD:-}"

# CLI precedence tracking
WORKERS_EXPLICIT=false
RPC_EXPLICIT=false
CHAIN_ID_EXPLICIT=false
RATE_EXPLICIT=false
CONNECTIONS_EXPLICIT=false
DURATION_EXPLICIT=false
WS_EXPLICIT=false
SEED_KEY_EXPLICIT=false
SEED_PRIVATE_KEY_EXPLICIT=false
SCENARIO_EXPLICIT=false

PERPS_USE_MID_PRICE_FROM_CONFIG=""

log() { echo "[$(date +'%H:%M:%S')] $*" >&2; }
die() { log "ERROR: $*"; exit 1; }

show_help() {
  cat <<EOF
PerpX Perps Load Runner - Order-Based Load-Test Scenarios

Usage: $0 [OPTIONS]

Core options:
  --workers N        Number of workers to seed (default: $WORKERS)
  --connections N    Connections per endpoint (default: $CONNECTIONS)
  --duration N       Test duration in seconds (default: $DURATION)
  --rate N           TPS per connection (default: $RATE)
  --scenario NAME    Scenario preset (simple_perps | maker_taker | stress_mixed | advanced_perps)
  --rng-seed N       Base RNG seed (maps to LOADTEST_PERPS_RNG_SEED)

Network / config:
  --rpc URL          RPC endpoint (overrides env/config)
  --ws URL           WebSocket endpoint (overrides env/config)
  --chain-id ID      Chain ID (overrides env/config)
  --config PATH      YAML config file (seed/loadtest.*; CLI > config > env > defaults)

Seeding / build:
  --skip-seed        Skip seeding
  --skip-build       Skip building the binary
  --seed-key VALUE   Seed key / mnemonic (overrides config seed.seedKey)
  --seed-private-key HEX   Hex-encoded private key (overrides seed-key)

Stats / debugging:
  --output-dir DIR   Output directory for stats CSV (default: $OUTPUT_DIR)
  --stats-output FILE  Explicit stats CSV path
  --no-stats         Disable writing stats CSV
  --check-txs        Enable per-transaction status checks (via REST)
  --help             Show this help message
EOF
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --workers)          WORKERS="$2"; WORKERS_EXPLICIT=true; shift 2 ;;
      --connections)      CONNECTIONS="$2"; CONNECTIONS_EXPLICIT=true; shift 2 ;;
      --duration)         DURATION="$2"; DURATION_EXPLICIT=true; shift 2 ;;
      --rate)             RATE="$2"; RATE_EXPLICIT=true; shift 2 ;;
      --scenario)         SCENARIO="$2"; SCENARIO_EXPLICIT=true; shift 2 ;;
      --rng-seed)         RNG_SEED="$2"; shift 2 ;;
      --output-dir)       OUTPUT_DIR="$2"; shift 2 ;;
      --config)           CONFIG_FILE="$2"; shift 2 ;;
      --stats-output)     STATS_OUTPUT_FILE="$2"; shift 2 ;;
      --no-stats)         NO_STATS_OUTPUT="true"; shift ;;
      --skip-seed)        SKIP_SEED="true"; shift ;;
      --skip-build)       SKIP_BUILD="true"; shift ;;
      --seed-key)         SEED_KEY="$2"; SEED_KEY_EXPLICIT=true; shift 2 ;;
      --seed-private-key) SEED_PRIVATE_KEY="$2"; SEED_PRIVATE_KEY_EXPLICIT=true; shift 2 ;;
      --rpc)              RPC_URL="$2"; RPC_EXPLICIT=true; shift 2 ;;
      --ws)               WS_URL="$2"; WS_EXPLICIT=true; shift 2 ;;
      --chain-id)         CHAIN_ID="$2"; CHAIN_ID_EXPLICIT=true; shift 2 ;;
      --check-txs)        CHECK_TXS="true"; shift ;;
      --help|-h)          show_help; exit 0 ;;
      *)                  die "Unknown option: $1" ;;
    esac
  done
}

build_binary_if_needed() {
  if [ "$SKIP_BUILD" != "true" ]; then
    log "Building perpx-load-test binary into $BINARY"
    mkdir -p "$BUILD_DIR"
    (cd "$REPO_ROOT" && go build -o "$BINARY" ./cmd/perpx-load-test)
  else
    log "Skipping build (SKIP_BUILD=true)"
  fi

  [ -x "$BINARY" ] || die "binary not found or not executable at $BINARY"
}

merge_config_from_yaml() {
  [ -n "$CONFIG_FILE" ] && [ -f "$CONFIG_FILE" ] || return 0

  local LOADTEST_VARS
  LOADTEST_VARS=$("$BINARY" config print-loadtest --config "$CONFIG_FILE" 2>/dev/null) || true
  [ -n "$LOADTEST_VARS" ] || return 0

  while IFS= read -r line; do
    case "$line" in
      RATE=*)
        [ "$RATE_EXPLICIT" != "true" ] && eval "$line" ;;
      CONNECTIONS=*)
        [ "$CONNECTIONS_EXPLICIT" != "true" ] && eval "$line" ;;
      DURATION=*)
        [ "$DURATION_EXPLICIT" != "true" ] && eval "$line" ;;
      BROADCAST_TX_METHOD=*)
        [ -z "$BROADCAST_TX_METHOD" ] && eval "$line" ;;
      WS_URL=*)
        [ "$WS_EXPLICIT" != "true" ] && eval "$line" ;;
      RPC_URL=*)
        [ "$RPC_EXPLICIT" != "true" ] && eval "$line" ;;
      REST_URL=*)
        eval "$line" ;;
      SEED_WORKERS=*)
        if [ "$WORKERS_EXPLICIT" != "true" ]; then
          eval "$line"
          WORKERS="$SEED_WORKERS"
        fi ;;
      PERPS_SCENARIO=*)
        if [ "$SCENARIO_EXPLICIT" != "true" ]; then
          eval "$line"
          SCENARIO="$PERPS_SCENARIO"
        fi ;;
      PERPS_USE_MID_PRICE=*)
        eval "$line"
        PERPS_USE_MID_PRICE_FROM_CONFIG="true" ;;
    esac
  done <<EOF
$LOADTEST_VARS
EOF
}

init_network() {
  [ -n "$RPC_URL" ] || die "LOADTEST_RPC is not set. Configure via env, YAML, or --rpc."

  if [ -z "$CHAIN_ID" ]; then
    local RPC_CHAIN_ID
    RPC_CHAIN_ID=$(curl -s "$RPC_URL/status" | sed -n 's/.*\"network\":\"\([^"]*\)\".*/\1/p' | head -n1)
    if [ -n "$RPC_CHAIN_ID" ] && [ "$RPC_CHAIN_ID" != "null" ]; then
      log "Inferred chain-id from RPC status: $RPC_CHAIN_ID"
      CHAIN_ID="$RPC_CHAIN_ID"
    else
      die "CHAIN_ID is not set and could not be inferred from $RPC_URL/status"
    fi
  fi

  [ -n "$WS_URL" ] || die "WS_URL is not set. Configure via env, YAML, or --ws."

  log "Checking RPC endpoint: $RPC_URL"
  local RPC_STATUS_JSON
  RPC_STATUS_JSON=$(curl -fsS "$RPC_URL/status" 2>/dev/null || true)
  [ -n "$RPC_STATUS_JSON" ] || die "cannot reach RPC at $RPC_URL (is the network running?)"

  local RPC_CHAIN_ID
  RPC_CHAIN_ID=$(printf '%s\n' "$RPC_STATUS_JSON" | sed -n 's/.*\"network\":\"\([^"]*\)\".*/\1/p' | head -n1)
  if [ -n "$RPC_CHAIN_ID" ]; then
    if [ "$CHAIN_ID" = "localperpxprotocol" ] && [ "$RPC_CHAIN_ID" != "$CHAIN_ID" ]; then
      log "Auto-detected chain-id from RPC status: $RPC_CHAIN_ID (was $CHAIN_ID)"
      CHAIN_ID="$RPC_CHAIN_ID"
    fi
  fi

  export LOADTEST_CHAIN_ID="$CHAIN_ID"
  export LOADTEST_RPC="$RPC_URL"
}

init_perps_env() {
  if [ -z "${LOADTEST_PERPS_USE_MID_PRICE:-}" ]; then
    if [ -n "$PERPS_USE_MID_PRICE_FROM_CONFIG" ]; then
      export LOADTEST_PERPS_USE_MID_PRICE="$PERPS_USE_MID_PRICE"
    else
      export LOADTEST_PERPS_USE_MID_PRICE="true"
    fi
  fi

  if [ -z "${LOADTEST_PERPS_MARKET_DATA_URL:-}" ]; then
    if [ -n "$REST_URL" ]; then
      export LOADTEST_PERPS_MARKET_DATA_URL="$REST_URL"
    else
      export LOADTEST_PERPS_MARKET_DATA_URL="http://localhost:41317"
    fi
  fi

  [ -n "${LOADTEST_PERPS_MARKET_DATA_CACHE_TTL_SECONDS:-}" ] \
    || export LOADTEST_PERPS_MARKET_DATA_CACHE_TTL_SECONDS="1"

  if [ -z "${LOADTEST_REST_URL:-}" ]; then
    if [ -n "$REST_URL" ]; then
      export LOADTEST_REST_URL="$REST_URL"
    elif [ -n "${LOADTEST_PERPS_MARKET_DATA_URL:-}" ]; then
      export LOADTEST_REST_URL="$LOADTEST_PERPS_MARKET_DATA_URL"
    fi
  fi

  if [ -n "$RNG_SEED" ]; then
    export LOADTEST_PERPS_RNG_SEED="$RNG_SEED"
  fi
}

compute_required_fund_amount() {
  REQUIRED_PER_WORKER_BANK=""

  if command -v python3 >/dev/null 2>&1; then
    REQUIRED_PER_WORKER_BANK=$(python3 - <<'PY'
import os

W = int(os.environ.get("WORKERS", "0") or "0")
C = int(os.environ.get("CONNECTIONS", "0") or "0")
R = int(os.environ.get("RATE", "0") or "0")
D = int(os.environ.get("DURATION", "0") or "0")

if C <= 0:
    C = 1
W_conn = W // C
if W_conn <= 0:
    W_conn = 1

txs_per_conn = R * D
base_sends_per_worker = (txs_per_conn + W_conn - 1) // W_conn
max_sends_per_worker = base_sends_per_worker * 2

gas_limit = 200_000
min_gas_price = 25_000_000_000  # 25e9
fee_amount = gas_limit * min_gas_price
per_tx_cost = fee_amount + 1

required = per_tx_cost * max_sends_per_worker
required = (required * 11) // 10  # +10% margin

print(required)
PY
    )
  else
    local GAS_LIMIT=200000
    local MIN_GAS_PRICE=25000000000
    local FEE_AMOUNT=$((GAS_LIMIT * MIN_GAS_PRICE))
    local SEND_AMOUNT=1
    local PER_TX_COST=$((FEE_AMOUNT + SEND_AMOUNT))

    local W="$WORKERS"
    local C="$CONNECTIONS"
    [ "$C" -le 0 ] && C=1
    local W_CONN=$((W / C))
    [ "$W_CONN" -le 0 ] && W_CONN=1

    local TXS_PER_CONN=$((RATE * DURATION))
    local BASE_SENDS_PER_WORKER=$(((TXS_PER_CONN + W_CONN - 1) / W_CONN))
    local MAX_SENDS_PER_WORKER=$((BASE_SENDS_PER_WORKER * 2))

    REQUIRED_PER_WORKER_BANK=$((PER_TX_COST * MAX_SENDS_PER_WORKER))
    REQUIRED_PER_WORKER_BANK=$(((REQUIRED_PER_WORKER_BANK * 11) / 10))
  fi

  if [ -z "$CONFIG_FILE" ] && [ -n "$REQUIRED_PER_WORKER_BANK" ]; then
    export LOADTEST_FUND_AMOUNT="${REQUIRED_PER_WORKER_BANK}${LOADTEST_DENOM:-aperpx}"
    log "Computed fund amount per worker for this perps run: $LOADTEST_FUND_AMOUNT"
  elif [ -n "$CONFIG_FILE" ]; then
    log "Using fund amount from config file (seed.fundAmount)"
  elif [ -n "$REQUIRED_PER_WORKER_BANK" ]; then
    log "Warning: failed to compute dynamic LOADTEST_FUND_AMOUNT; falling back to default"
  fi
}

seed_accounts_if_needed() {
  if [ "$SKIP_SEED" = "true" ]; then
    log "Skipping seeding (SKIP_SEED=true)"
    return
  fi

  if [ -n "$CONFIG_FILE" ]; then
    log "Seeding perps accounts (workers/rpc/chain-id/fundAmount from config: $CONFIG_FILE)"
  else
    log "Seeding perps accounts (workers: $WORKERS, chain-id: $CHAIN_ID, rpc: $RPC_URL)"
  fi

  local SEED_ARGS=(--mode perps)

  if [ -n "$CONFIG_FILE" ]; then
    SEED_ARGS+=(--config "$CONFIG_FILE")
    "$WORKERS_EXPLICIT"  && SEED_ARGS+=(--workers "$WORKERS")
    "$RPC_EXPLICIT"      && SEED_ARGS+=(--rpc "$RPC_URL")
    "$CHAIN_ID_EXPLICIT" && SEED_ARGS+=(--chain-id "$CHAIN_ID")
  else
    SEED_ARGS+=(--workers "$WORKERS" --rpc "$RPC_URL" --chain-id "$CHAIN_ID")
  fi

  if [ -n "$SEED_PRIVATE_KEY" ]; then
    log "Using seed private key for seeding"
    SEED_ARGS+=(--seed-private-key "$SEED_PRIVATE_KEY")
  elif [ -n "$CONFIG_FILE" ] && [ "$SEED_KEY_EXPLICIT" != "true" ]; then
    log "Using seed key from config file"
  else
    if [ -z "$SEED_KEY" ]; then
      SEED_KEY="alice"
    fi
    log "Using seed key: $SEED_KEY"
    SEED_ARGS+=(--seed-key "$SEED_KEY")
  fi

  if [ -n "$CONFIG_FILE" ]; then
    (
      unset LOADTEST_FUND_AMOUNT LOADTEST_BATCH_SIZE LOADTEST_WORKERS \
            LOADTEST_RPC LOADTEST_CHAIN_ID LOADTEST_DENOM \
            LOADTEST_PERPS_SEED_MODE LOADTEST_PERPS_DEPOSIT 2>/dev/null || true
      "$BINARY" seed "${SEED_ARGS[@]}"
    )
  else
    "$BINARY" seed "${SEED_ARGS[@]}"
  fi
}

run_perps_loadtest() {
  log "Running perps load test:"
  log "  Scenario     : $SCENARIO"
  log "  Workers      : $WORKERS (seeded)"
  log "  Connections  : $CONNECTIONS"
  log "  Duration     : $DURATION s"
  log "  Rate         : $RATE tx/s per connection"
  log "  WS endpoint  : $WS_URL"

  export LOADTEST_PERPS_SCENARIO="$SCENARIO"

  if [ "$NO_STATS_OUTPUT" = "true" ]; then
    STATS_OUTPUT_FILE=""
  else
    if [ -z "$STATS_OUTPUT_FILE" ]; then
      TIMESTAMP="$(date +'%Y%m%d_%H%M%S')"
      mkdir -p "$OUTPUT_DIR"
      STATS_OUTPUT_FILE="$OUTPUT_DIR/perps-loadtest-stats-${TIMESTAMP}.csv"
    else
      mkdir -p "$(dirname "$STATS_OUTPUT_FILE")"
    fi
  fi
  if [ -n "$STATS_OUTPUT_FILE" ]; then
    log "  Stats output : $STATS_OUTPUT_FILE"
  fi

  if [ "$CHECK_TXS" = "true" ]; then
    export LOADTEST_PERPS_MONITOR_TX_STATUS="true"
    [ -n "${LOADTEST_PERPS_TX_MONITOR_SAMPLE_RATE:-}" ] || export LOADTEST_PERPS_TX_MONITOR_SAMPLE_RATE="1"
    [ -n "$BROADCAST_TX_METHOD" ] || BROADCAST_TX_METHOD="sync"
  else
    [ -n "$BROADCAST_TX_METHOD" ] || BROADCAST_TX_METHOD="async"
  fi

  local LOADTEST_ARGS=()
  if [ -n "$CONFIG_FILE" ]; then
    LOADTEST_ARGS+=(--config "$CONFIG_FILE")
  fi

  LOADTEST_ARGS+=(
    --client-factory perpx-perps
    --connections "$CONNECTIONS"
    --rate "$RATE"
    --time "$DURATION"
    --broadcast-tx-method "$BROADCAST_TX_METHOD"
    --endpoints "$WS_URL"
  )
  if [ -n "$STATS_OUTPUT_FILE" ]; then
    LOADTEST_ARGS+=(--stats-output "$STATS_OUTPUT_FILE")
  fi

  log "Executing perps load test binary..."
  log "  Command: $BINARY ${LOADTEST_ARGS[*]}"

  local START_TIME END_TIME START_HEIGHT END_HEIGHT
  START_TIME=$(date +%s)
  START_HEIGHT=""
  if command -v jq >/dev/null 2>&1; then
    START_HEIGHT=$(curl -s "$RPC_URL/status" | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo "")
  fi

  "$BINARY" "${LOADTEST_ARGS[@]}"
  local EXIT_CODE=$?
  if [ $EXIT_CODE -ne 0 ]; then
    die "perps load test failed with exit code $EXIT_CODE"
  fi

  END_TIME=$(date +%s)
  END_HEIGHT=""
  if command -v jq >/dev/null 2>&1; then
    END_HEIGHT=$(curl -s "$RPC_URL/status" | jq -r '.result.sync_info.latest_block_height' 2>/dev/null || echo "")
  fi

  summarize_results "$START_TIME" "$END_TIME" "$START_HEIGHT" "$END_HEIGHT"
}

summarize_results() {
  local START_TIME="$1" END_TIME="$2" START_HEIGHT="$3" END_HEIGHT="$4"
  local ELAPSED=$((END_TIME - START_TIME))

  echo ""
  echo "=========================================="
  echo "Perps Load Test Summary"
  echo "=========================================="
  echo "Configuration:"
  local TOTAL_ENDPOINTS=1
  local TOTAL_CONNECTIONS=$((CONNECTIONS * TOTAL_ENDPOINTS))
  local WORKERS_PER_CONN="N/A"
  if [ "$TOTAL_CONNECTIONS" -gt 0 ] 2>/dev/null && [ "$WORKERS" -gt 0 ] 2>/dev/null; then
    if [ $((WORKERS % TOTAL_ENDPOINTS)) -eq 0 ] 2>/dev/null; then
      WORKERS_PER_CONN=$((WORKERS / TOTAL_ENDPOINTS))
    fi
  fi
  local ATTEMPTED_TPS=$((CONNECTIONS * RATE * TOTAL_ENDPOINTS))
  echo "  Scenario:          $SCENARIO"
  echo "  Workers (seeded):  $WORKERS"
  echo "  Endpoints:         $TOTAL_ENDPOINTS"
  echo "  Connections:       $CONNECTIONS (total: ${TOTAL_CONNECTIONS})"
  echo "  Workers/connection G: $WORKERS_PER_CONN"
  echo "  Duration:          ${DURATION}s"
  echo "  Rate per conn:     $RATE tx/s"
  echo "  Total attempted:   ${ATTEMPTED_TPS} tx/s"
  echo ""

  if [ -n "$START_HEIGHT" ] && [ -n "$END_HEIGHT" ] && [ "$START_HEIGHT" != "null" ] && [ "$END_HEIGHT" != "null" ]; then
    local BLOCKS_PROCESSED=$((END_HEIGHT - START_HEIGHT))
    echo "Execution:"
    echo "  Start height:      $START_HEIGHT"
    echo "  End height:        $END_HEIGHT"
    echo "  Blocks processed:  $BLOCKS_PROCESSED"
    echo "  Elapsed time:      ${ELAPSED}s"
    echo ""
  else
    echo "Execution:"
    echo "  Elapsed time:      ${ELAPSED}s"
    echo "  (Block heights unavailable; install 'jq' for block-level metrics)"
    echo ""
  fi

  local TOTAL_TIME="0" TOTAL_TXS="0" TOTAL_BYTES="0"
  local AVG_TX_RATE="0" AVG_DATA_RATE="0" AVG_TX_SIZE="0"
  local AVG_BLOCK_TIME="0" TXS_PER_BLOCK="0" EFFICIENCY="0"
  ATTEMPTED_TPS=$((CONNECTIONS * RATE))
  local ATTEMPTED_TXS=$((CONNECTIONS * RATE * DURATION))

  if [ -n "$STATS_OUTPUT_FILE" ] && [ -f "$STATS_OUTPUT_FILE" ]; then
    TOTAL_TIME=$(grep "total_time" "$STATS_OUTPUT_FILE" | cut -d',' -f2 || echo "0")
    TOTAL_TXS=$(grep "total_txs" "$STATS_OUTPUT_FILE" | cut -d',' -f2 || echo "0")
    TOTAL_BYTES=$(grep "total_bytes" "$STATS_OUTPUT_FILE" | cut -d',' -f2 || echo "0")
    AVG_TX_RATE=$(grep "avg_tx_rate" "$STATS_OUTPUT_FILE" | cut -d',' -f2 || echo "0")
    AVG_DATA_RATE=$(grep "avg_data_rate" "$STATS_OUTPUT_FILE" | cut -d',' -f2 || echo "0")
    AVG_TX_SIZE=$(grep "avg_tx_size" "$STATS_OUTPUT_FILE" | cut -d',' -f2 || echo "0")

    if [ -n "$START_HEIGHT" ] && [ -n "$END_HEIGHT" ] && [ "$START_HEIGHT" != "null" ] && [ "$END_HEIGHT" != "null" ]; then
      local BLOCKS_PROCESSED=$((END_HEIGHT - START_HEIGHT))
      if [ "$BLOCKS_PROCESSED" -gt 0 ] 2>/dev/null; then
        AVG_BLOCK_TIME=$(awk "BEGIN {printf \"%.3f\", $TOTAL_TIME / $BLOCKS_PROCESSED}")
        TXS_PER_BLOCK=$(awk "BEGIN {printf \"%.2f\", $TOTAL_TXS / $BLOCKS_PROCESSED}")
      fi
    fi

    if [ "$ATTEMPTED_TPS" -gt 0 ] 2>/dev/null; then
      EFFICIENCY=$(awk "BEGIN {printf \"%.2f\", ($AVG_TX_RATE * 100) / $ATTEMPTED_TPS}")
    fi

    echo "Results (from CSV):"
    echo "  total_time:        $TOTAL_TIME seconds"
    echo "  total_txs:         $TOTAL_TXS"
    echo "  total_bytes:       $TOTAL_BYTES bytes"
    echo "  avg_tx_rate:       $AVG_TX_RATE tx/s"
    echo "  avg_data_rate:     $AVG_DATA_RATE bytes/s"
    echo "  avg_tx_size:       $AVG_TX_SIZE bytes/tx"
    if [ "$AVG_BLOCK_TIME" != "0" ]; then
      echo "  avg_block_time:    ${AVG_BLOCK_TIME}s"
      echo "  txs_per_block:     $TXS_PER_BLOCK"
    fi
    if [ "$ATTEMPTED_TPS" -gt 0 ] 2>/dev/null; then
      echo "  actual_tps:        $AVG_TX_RATE (vs attempted $ATTEMPTED_TPS tx/s, $EFFICIENCY% achieved)"
    fi
    echo ""
  else
    if [ -n "$STATS_OUTPUT_FILE" ]; then
      echo "Results: stats CSV not found at $STATS_OUTPUT_FILE (no detailed metrics available)"
      echo ""
    fi
  fi

  if [ -n "$STATS_OUTPUT_FILE" ]; then
    echo "Stats CSV saved to: $STATS_OUTPUT_FILE"
  fi

  if [ -n "$STATS_OUTPUT_FILE" ] && [ -f "$STATS_OUTPUT_FILE" ]; then
    [ -n "${TIMESTAMP:-}" ] || TIMESTAMP="$(date +%Y%m%d_%H%M%S)"
    local RESULT_LINE="RESULT timestamp=${TIMESTAMP} avg_tps=${AVG_TX_RATE} attempted_tps=${ATTEMPTED_TPS} total_txs=${TOTAL_TXS} total_time_s=${TOTAL_TIME} workers=${WORKERS} connections=${CONNECTIONS} rate_per_connection=${RATE} ws_endpoint=${WS_URL}"
    echo "$RESULT_LINE"
  fi
}

main() {
  parse_args "$@"
  build_binary_if_needed
  merge_config_from_yaml
  init_network
  init_perps_env
  compute_required_fund_amount
  seed_accounts_if_needed
  run_perps_loadtest
}

main "$@"
