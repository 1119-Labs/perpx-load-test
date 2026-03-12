# PerpX Load Test

A comprehensive load testing tool for the PerpX Protocol blockchain, built on top of [cometbft-load-test](https://github.com/cometbft/cometbft-load-test). This tool enables developers to stress test PerpX localnet deployments by generating and broadcasting bank send transactions at scale.

## Overview

PerpX Load Test is designed to help developers and operators:
- **Stress test** PerpX Protocol blockchain networks
- **Measure performance** under various load conditions
- **Validate scalability** of the network infrastructure
- **Test transaction throughput** and network stability

The tool consists of two main components:
1. **Seed Command**: Pre-funds test accounts with tokens before running load tests
2. **Load Test Engine**: Generates and broadcasts transactions at configurable rates

## Features

- 🚀 **High-Throughput Testing**: Generate and broadcast thousands of transactions per second
- 🔑 **Deterministic Account Generation**: Each worker uses a deterministically generated account for reproducible tests
- 💰 **Automatic Account Seeding**: Pre-fund test accounts with a single command
- 📊 **Real-time Statistics**: Monitor transaction rates, success rates, and latency
- 🔌 **WebSocket Support**: Efficient connection management via WebSocket endpoints
- ⚙️ **Flexible Configuration**: Customize connection count, transaction rate, duration, and more
- 🎯 **Bank Send Strategy**: Specialized client for testing bank send transactions
- 📈 **Perps Order Scenarios**: Load test perpetual order matching engine with realistic order scenarios (place, cancel, amend, close)

## Prerequisites

- **Go 1.25.4** or later
- **PerpX Protocol localnet** running and accessible
- **Network access** to the PerpX RPC, gRPC, and REST endpoints

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/1119-Labs/perpx-load-test.git
cd perpx-load-test

# Build the binary
go build -o ./bin/perpx-load-test ./cmd/perpx-load-test
```

## Quick Start

### Option 1: Automated Benchmark Script (Recommended)

The easiest way to run benchmarks is using the automated script that handles both seeding and load testing:

```bash
# Run with localnet config file
# First, need to provide the seedPrivateKey into the config/localnet/config-bank.yaml
# or config/localnet/config-perps.yaml based on your run

# Then run:
# For benchmark send tx:
./scripts/run_benchmark.sh --config ./config/localnet/config-bank.yaml

# For benchmark place orders
./scripts/run_perps_load.sh --config ./config/localnet/config-perps.yaml
```

```bash
# Basic benchmark (10 workers, 30 seconds, 50 TPS per worker)
./scripts/run_benchmark.sh

# Higher load test
./scripts/run_benchmark.sh --workers 20 --duration 60 --rate 100

# Skip seeding if accounts are already funded
./scripts/run_benchmark.sh --skip-seed --workers 50 --duration 120 --rate 200

# Use TUI for real-time monitoring
./scripts/run_benchmark.sh --tui --workers 10 --duration 60 --rate 100
```

The script automatically:
- Checks if the localnet is running
- Builds the binary (unless `--skip-build` is used)
- Seeds test accounts (unless `--skip-seed` is used)
- Runs the load test
- Generates CSV and JSON results in `./exported/`

See the [Automated Benchmark Script](#automated-benchmark-script) section for full documentation.

### Option 2: Manual Commands

#### 1. Seed Test Accounts

Before running a load test, you need to fund the test accounts. The seed command creates and funds accounts for each worker:

```bash
# Basic usage
perpx-load-test seed --workers 10

# With custom configuration
perpx-load-test seed \
  --workers 50 \
  --rpc http://localhost:36657 \
  --chain-id localperpxprotocol \
  --fund-amount 1000000000000000000aperpx \
  --batch-size 50
```

The seed command will:
- Generate deterministic accounts for each worker
- Check which accounts need funding
- Fund accounts in batches via multi-message transactions
- Verify all accounts are properly funded

### Configuration Precedence

Configuration values can come from multiple places. The effective config for a
run is resolved using the following precedence:

1. **CLI flags** (e.g. `--connections`, `--rate`, `--time`, `--client-factory`)
2. **YAML config file** (`--config`, keys under `loadtest.*`, `seed.*`, `perps.*`)
3. **Environment variables** (e.g. `LOADTEST_RPC`, `LOADTEST_CHAIN_ID`, `WS_URL`, `WS_ENDPOINTS`, `LOADTEST_DENOM`, `LOADTEST_REST_URL`, perps-specific `LOADTEST_PERPS_*`)
4. **Code defaults** (hard-coded defaults in the CLI and strategies)

The core binary applies this precedence when building its internal
`loadtest.Config`:

- Flags are bound directly into `loadtest.Config`.
- YAML (if `--config` is provided) is applied only for fields whose flags were
  **not** explicitly set.
- Remaining zero-value fields may then be filled from environment variables
  (for example: `LOADTEST_CHAIN_ID`, `LOADTEST_DENOM`, `WS_ENDPOINTS` /
  `WS_URL`, `LOADTEST_REST_URL`).

The shell scripts follow the same intent:

- `scripts/run_benchmark.sh` and `scripts/run_perps_load.sh` first accept
  **flags**, then optionally read a YAML file via `perpx-load-test config
  print-loadtest`, then fall back to **environment** (e.g. `.env.localnet-dev`)
  and finally to script defaults.

#### 2. Run Load Test

Once accounts are seeded, run the load test:

```bash
# Basic load test (1 connection, 1000 tx/s, 60 seconds)
perpx-load-test \
  --connections 1 \
  --rate 1000 \
  --time 60 \
  --endpoints ws://localhost:36657/websocket

# High-throughput test (10 connections, 5000 tx/s, 120 seconds)
perpx-load-test \
  --connections 10 \
  --rate 5000 \
  --time 120 \
  --endpoints ws://localhost:36657/websocket

# With TUI (Terminal User Interface) for real-time stats
perpx-load-test \
  --connections 5 \
  --rate 2000 \
  --time 60 \
  --ui tui \
  --endpoints ws://localhost:36657/websocket
```

## Usage

### Automated Benchmark Script

The `run_benchmark.sh` script automates the entire benchmark process, from account seeding to running load tests and generating results. It's the recommended way to run benchmarks.

#### Features

- **Automatic Setup**: Builds binary, seeds accounts, and runs tests in one command
- **Result Generation**: Automatically generates CSV and JSON result files
- **Multiple Endpoints**: Supports testing against multiple RPC endpoints simultaneously
- **UI Options**: Supports quiet mode, verbose logs, and full-screen TUI
- **Error Handling**: Automatically detects and attempts to fix Go version mismatches

#### Options

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--workers` | `-w` | Number of workers (connections per endpoint) | `10` |
| `--duration` | `-d` | Test duration in seconds | `30` |
| `--rate` | `-r` | Transactions per second per worker | `50` |
| `--skip-seed` | | Skip account seeding | `false` |
| `--skip-build` | | Skip building the binary | `false` |
| `--output-dir` | `-o` | Output directory for results | `./exported` |
| `--seed-private-key` | `-p` | Hex-encoded private key for seeding | - |
| `--seed-key` | `-k` | Key name or mnemonic for seeding |  |
| `--ws-endpoints` | | Comma-separated WebSocket endpoints | `ws://localhost:36657/websocket` |
| `--quiet` | | Quiet UI (progress line only) | `true` |
| `--verbose-logs` | | Stream full logs to terminal | `false` |
| `--tui` | | Full-screen real-time TUI | `false` |
| `--help` | `-h` | Show help message | - |

#### Environment Variables

All options can also be set via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `WORKERS` | Number of workers | `10` |
| `DURATION` | Test duration (seconds) | `30` |
| `RATE` | TPS per worker | `50` |
| `SKIP_SEED` | Skip seeding (`true`/`false`) | `false` |
| `SKIP_BUILD` | Skip build (`true`/`false`) | `false` |
| `OUTPUT_DIR` | Output directory | `./exported` |
| `SEED_PRIVATE_KEY` | Hex-encoded private key | - |
| `SEED_KEY` | Seed key/mnemonic |  |
| `CHAIN_ID` | Chain ID | `localperpxprotocol` |
| `RPC_URL` | RPC endpoint | `http://localhost:36657` |
| `WS_URL` | WebSocket endpoint | `ws://localhost:36657/websocket` |
| `WS_ENDPOINTS` | Comma-separated WebSocket endpoints | `$WS_URL` |
| `QUIET` | Quiet UI (`true`/`false`) | `true` |
| `SHOW_LIVE_LOGS` | Stream logs (`true`/`false`) | `false` |
| `UI_MODE` | UI mode (`plain`/`tui`) | `plain` |

#### Examples

```bash
# Basic benchmark with defaults
./scripts/run_benchmark.sh

# Canonical Scenario A: Bank high-throughput smoke test
#  - 10 workers, 30s, 50 TPS/worker (15k attempted txs total)
./scripts/run_benchmark.sh \
  --workers 10 \
  --duration 30 \
  --rate 50

# Canonical Scenario B: Bank sustained throughput
#  - 20 workers, 120s, 100 TPS/worker (240k attempted txs total)
./scripts/run_benchmark.sh \
  --workers 20 \
  --duration 120 \
  --rate 100

# Canonical Scenario C: Multi-endpoint regression
#  - 10 workers/endpoint, 60s, 75 TPS/worker across 2 RPCs
WS_ENDPOINTS="ws://localhost:36657/websocket,ws://localhost:36658/websocket" \
  ./scripts/run_benchmark.sh \
  --workers 10 \
  --duration 60 \
  --rate 75

# High-throughput test
./scripts/run_benchmark.sh --workers 20 --duration 120 --rate 200

# Test against multiple endpoints
WS_ENDPOINTS="ws://localhost:36657/websocket,ws://localhost:36658/websocket" \
  ./scripts/run_benchmark.sh --workers 10 --duration 60

# Use existing binary and skip seeding
./scripts/run_benchmark.sh --skip-build --skip-seed --workers 50

# Full-screen TUI for real-time monitoring
./scripts/run_benchmark.sh --tui --workers 10 --duration 300 --rate 100

# Verbose output with full logs
./scripts/run_benchmark.sh --verbose-logs --workers 5 --duration 30

# Custom output directory
./scripts/run_benchmark.sh --output-dir ./my-results --workers 20
```

#### Output Files

The script generates timestamped output files in the output directory:

- `loadtest-stats-YYYYMMDD_HHMMSS.csv` - CSV statistics file
- `loadtest-results-YYYYMMDD_HHMMSS.json` - JSON summary with configuration and results
- `loadtest-run-YYYYMMDD_HHMMSS.log` - Full load test log
- `loadtest-seed-YYYYMMDD_HHMMSS.log` - Account seeding log (if seeding was performed)

#### Result Summary

After completion, the script displays a summary including:
- Configuration (workers, duration, rate, endpoints)
- Execution metrics (start/end height, blocks processed, elapsed time)
- Performance metrics (TPS, total transactions, efficiency)
- File locations for detailed results

#### Requirements

- `curl` - For checking localnet status
- `jq` - For parsing JSON results (optional, for JSON output)
- `go` - For building the binary (unless `--skip-build` is used)
- PerpX localnet running and accessible

### Seed Command

The `seed` command prepares test accounts by funding them with tokens.

#### Options

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--workers` | `-w` | Number of workers to seed | `10` |
| `--seed-key` | `-k` | Key name or mnemonic for seeding | |
| `--seed-private-key` | `-p` | Hex-encoded private key (takes precedence) | - |
| `--rpc` | `-r` | RPC endpoint | `http://localhost:36657` |
| `--chain-id` | | Chain ID | `localperpxprotocol` |
| `--denom` | | Token denomination | `aperpx` |
| `--fund-amount` | | Amount to fund each account | `1000000000000000000aperpx` |
| `--batch-size` | | Accounts per transaction | `50` |
| `--mode` | | Seeding mode (`bank` or `perps`) | `bank` |
| `--perps-deposit` | | USDC quantums deposited to each subaccount (perps mode) | `10000000000` |
| `--rest-url` | | REST base URL for balance/account queries | unset (uses RPC-derived defaults) |
| `--config` | | Path to YAML config file (optional) | - |
| `--help` | `-h` | Show help message | - |

#### Examples

```bash
# Seed 100 workers with default settings
perpx-load-test seed --workers 100

# Use a custom mnemonic
perpx-load-test seed \
  --seed-key "your twelve word mnemonic phrase here goes like this example"

# Use a private key directly
perpx-load-test seed \
  --seed-private-key "0x1234567890abcdef..." \
  --workers 50

# Custom RPC and funding amount
perpx-load-test seed \
  --rpc http://192.168.1.100:36657 \
  --fund-amount 5000000aperpx \
  --workers 20
```

### Load Test Command

The main load test command generates and broadcasts transactions.

#### Options

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--client-factory` | | Client factory identifier (`perpx-bank`, `perpx-perps`) | `perpx-bank` |
| `--connections` | `-c` | Connections per endpoint | `1` |
| `--time` | `-T` | Test duration (seconds) | `60` |
| `--send-period` | `-p` | Send period (seconds) | `1` |
| `--rate` | `-r` | Transactions per second | `1000` |
| `--size` | `-s` | Transaction size (bytes) | `250` |
| `--count` | | Max transactions to send | `-1` (unlimited) |
| `--broadcast-tx-method` | | Broadcast method (`sync`, `async`, `commit`) | `async` |
| `--endpoints` | | Comma-separated WebSocket RPC endpoints | (required) |
| `--rest-url` | | Base REST URL for account/tx queries | unset (clients may derive) |
| `--chain-id` | | Chain ID for signing | `localperpxprotocol` |
| `--denom` | | Fee denomination | `aperpx` |
| `--ui` | | UI mode (`plain`, `tui`) | `plain` |
| `--verbose` | | Enable verbose logging | `false` |
| `--config` | | Path to YAML config file (optional; `loadtest.*` used as defaults) | - |

#### Examples

**Bank Send Transactions (Default):**

```bash
# Quick 30-second test
perpx-load-test --time 30 --rate 500 --endpoints ws://localhost:36657/websocket

# High-throughput test with multiple connections
perpx-load-test \
  --connections 10 \
  --rate 10000 \
  --time 300 \
  --endpoints ws://localhost:36657/websocket

# Test with TUI for real-time monitoring
perpx-load-test \
  --connections 5 \
  --rate 2000 \
  --time 120 \
  --ui tui \
  --endpoints ws://localhost:36657/websocket

# Limit total transaction count
perpx-load-test \
  --count 10000 \
  --rate 1000 \
  --endpoints ws://localhost:36657/websocket
```

**Perps Order Scenarios:**

See the [Perps Load Testing](#perps-load-testing) section below for detailed examples.

## Perps Load Testing

The `perpx-perps` client factory enables load testing of PerpX's perpetual order matching engine with realistic order scenarios.

### Prerequisites

1. **Localnet & indexer**: Run PerpX dev localnet (e.g. protocol repo `deployment/localnet-dev`: `make build`, `make init`, `make start`) and ensure the indexer stack is up. Optionally run **`./scripts/check_localnet_dev.sh`** to verify RPC and indexer before load tests.

2. **Seed Accounts**: Ensure test accounts are funded (same as bank sends):
   ```bash
   perpx-load-test seed --workers 10
   ```
   For perps, use **`--mode perps`** (or set `LOADTEST_PERPS_SEED_MODE=perps`) so subaccounts receive margin deposits.

3. **Configure Markets**: Set `LOADTEST_PERPS_MARKETS` to match your localnet `config.yml` (CLOB pair IDs, quantums, subticks). See [Configuration](#perps-configuration) below and [CLOB orderflow checklist](docs/clob-orderflow-bots-and-load-tests.md).

### Quick Start

```bash
# Simple perps scenario (default: mostly market orders)
LOADTEST_PERPS_SCENARIO=simple_perps \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 50 \
  --time 60 \
  --endpoints ws://localhost:36657/websocket

# Maker/taker scenario (60% limit orders, 20% cancels, 20% taker orders)
LOADTEST_PERPS_SCENARIO=maker_taker \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 100 \
  --time 120 \
  --endpoints ws://localhost:36657/websocket

# Stress test scenario (mixed actions with higher cancel/amend activity)
LOADTEST_PERPS_SCENARIO=stress_mixed \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 200 \
  --time 300 \
  --endpoints ws://localhost:36657/websocket
```

### Perps Configuration

Perps load testing is configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `LOADTEST_PERPS_SCENARIO` | Scenario preset: `simple_perps`, `maker_taker`, `stress_mixed`, or `advanced_perps` | `simple_perps` |
| `LOADTEST_PERPS_MARKETS` | Comma-separated market specs: `clobPairID:symbol:minQty-maxQty:minSubticks-maxSubticks` | `1:ETH-PERP:1-10:100-200` |
| `LOADTEST_PERPS_ACTION_WEIGHTS` | Override action weights: `place,cancel,amend,close,noop` (e.g., `80,5,5,10,0`) | Preset defaults |
| `LOADTEST_PERPS_MIN_LEVERAGE` | Minimum leverage (float) | Preset defaults |
| `LOADTEST_PERPS_MAX_LEVERAGE` | Maximum leverage (float) | Preset defaults |
| `LOADTEST_PERPS_MAX_TRACKED_ORDERS` | Max orders tracked per worker for cancels | `1024` |
| `LOADTEST_PERPS_BATCH_CANCEL_PCT` | Probability (0-100) that cancel actions use batch cancel instead of single cancel | `0` (preset-dependent) |
| `LOADTEST_PERPS_RNG_SEED` | Optional base RNG seed for deterministic perps behavior; each worker derives its own stream from this base | unset (time-based) |

Perps order flow now always uses a **single deterministic bench account per worker** (no per-connection/per-tx address modes). The seeded perps accounts created by `perpx-load-test seed --mode perps` are keyed by worker index and align directly with the perps client’s deterministic key derivation.

### Advanced Perps Features (Docs)

The perps implementation includes additional features beyond the original `docs/order-plan.md`:

- Error handling & robustness (retries/backoff, error metrics, optional tx-status polling)
- Advanced order types (long-term, conditional, TWAP, batch cancel)
- Market data integration (mid-price pricing, provider interface, caching)
- Position tracking interface (used for close sizing; worker-local model)

Start here:

- **[Advanced Features](docs/advanced-features.md)** (overview + links)
- **[Environment Variables](docs/environment-variables.md)** (full reference)

#### Market Configuration Format

Markets are specified as:
```
clobPairID:symbol:minQty-maxQty:minSubticks-maxSubticks
```

Example:
```bash
# Single market
LOADTEST_PERPS_MARKETS="1:ETH-PERP:1-10:100-200"

# Multiple markets
LOADTEST_PERPS_MARKETS="1:ETH-PERP:1-10:100-200,2:BTC-PERP:1-5:50-150"
```

#### Scenario Presets

- **`simple_perps`**: 1-2 markets, mostly market orders (80% place, 5% cancel, 5% amend, 10% close). Leverage: 1.0-5.0x. Batch cancel: 0% (disabled).
- **`maker_taker`**: Mix of limit orders and cancels (60% place, 20% cancel, 10% amend, 10% close). Leverage: 1.0-10.0x. Batch cancel: 10% of cancel actions.
- **`stress_mixed`**: High activity mix (50% place, 20% cancel, 15% amend, 10% close, 5% noop). Leverage: 1.0-15.0x. Batch cancel: 15% of cancel actions.

### Example Workflows

**1. Basic Perps Load Test:**
```bash
# Seed accounts
perpx-load-test seed --workers 20

# Run simple perps scenario
LOADTEST_PERPS_SCENARIO=simple_perps \
  perpx-load-test \
  --client-factory perpx-perps \
  --connections 5 \
  --rate 50 \
  --time 60 \
  --endpoints ws://localhost:36657/websocket
```

**2. Custom Action Weights:**
```bash
# Override action weights (80% place, 10% cancel, 5% amend, 5% close, 0% noop)
LOADTEST_PERPS_SCENARIO=simple_perps \
LOADTEST_PERPS_ACTION_WEIGHTS="80,10,5,5,0" \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 100 \
  --time 120 \
  --endpoints ws://localhost:36657/websocket
```

**3. Multiple Markets:**
```bash
LOADTEST_PERPS_MARKETS="1:ETH-PERP:1-10:100-200,2:BTC-PERP:1-5:50-150" \
LOADTEST_PERPS_SCENARIO=maker_taker \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 75 \
  --time 180 \
  --endpoints ws://localhost:36657/websocket
```

**4. Low-Rate Validation Test:**
```bash
# Run a short, low-rate test to validate transactions are accepted
LOADTEST_PERPS_SCENARIO=simple_perps \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 5 \
  --time 10 \
  --endpoints ws://localhost:36657/websocket
```

### Perps Order Types

The perps client generates the following order types based on scenario actions:

- **Place**: Market or limit orders (IOC for taker-style, POST_ONLY for maker-style)
- **Cancel**: Cancel existing tracked orders (single cancel or batch cancel)
- **Amend**: Modeled as replacement maker orders
- **Close**: Reduce-only IOC orders to close positions
- **Noop**: Margin deposits (doesn't affect order book)

#### Batch Cancel

When a cancel action is selected, the client may use batch cancel instead of single cancel based on the configured `LOADTEST_PERPS_BATCH_CANCEL_PCT` probability. Batch cancel can cancel up to 8 orders on the same market in a single transaction, which is more efficient than multiple single cancel transactions.

**Behavior:**
- Batch cancel is attempted when:
  - `LOADTEST_PERPS_BATCH_CANCEL_PCT` > 0
  - At least 2 tracked orders exist on the same CLOB pair
  - Random probability check passes
- If batch cancel cannot be performed (e.g., orders on different markets, insufficient orders), the client falls back to single cancel
- All canceled orders are automatically removed from tracking after batch cancel

**Example:**
```bash
# Enable batch cancel for 20% of cancel actions
LOADTEST_PERPS_BATCH_CANCEL_PCT=20 \
LOADTEST_PERPS_SCENARIO=maker_taker \
  perpx-load-test \
  --client-factory perpx-perps \
  --rate 100 \
  --endpoints ws://localhost:36657/websocket
```

## Environment Variables

You can configure the tool using environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `LOADTEST_SEED_KEY` | Seed key/mnemonic for seeding | |
| `LOADTEST_SEED_PRIVATE_KEY` | Hex-encoded private key for seeding | - |
| `LOADTEST_RPC` | RPC endpoint | `http://localhost:36657` |
| `LOADTEST_CHAIN_ID` | Chain ID | `localperpxprotocol` |
| `LOADTEST_DENOM` | Token denomination | `aperpx` |
| `LOADTEST_FUND_AMOUNT` | Amount to fund each account | `1000000aperpx` |
| `LOADTEST_SINK_ADDRESS` | Destination address for bank sends (single-receiver mode) | `perpx1kyfmupa8z5jtxgf5f4gt285sepeg6eqnzvs25m` |
| `LOADTEST_RECEIVER_POOL` | Set to `many` so each send uses a random receiver from the bench accounts (different senders → different receivers; avoids sequential tx dependency). Connection count unchanged. | unset (single sink) |

See [Perps Configuration](#perps-configuration) for perps-specific environment variables.

## Documentation

- **[CLOB orderflow & localnet-dev checklist](docs/clob-orderflow-bots-and-load-tests.md)**: How to run perps bots/load tests against dev localnet so the orderbook and candles stay populated; localnet-dev bring-up, config.yml alignment, indexer/candles verification, observability.
- **[Architecture Documentation](docs/architecture.md)**: Detailed architecture overview and design decisions
- **[Usage Examples](docs/examples/perps-load-testing.md)**: Practical examples and tutorials
- **[Performance Tuning Guide](docs/performance-tuning.md)**: Performance tuning strategies and best practices
- **[Testing Guide](docs/testing.md)**: How to run unit tests, integration tests, and benchmarks
- **[Order Plan](docs/order-plan.md)**: Original design plan (see [deep comparison](docs/order-plan-deep-comparison.md) for implementation differences)
- **[Advanced Features](docs/advanced-features.md)**: Advanced perps feature documentation
- **[Environment Variables](docs/environment-variables.md)**: Comprehensive environment variable reference

## Architecture

### Architecture & Boundaries (Perps)

The perps load path is structured around a few clear boundaries:

- **Client ↔ Chain boundary**: `PerpxPerpsClient` talks to the chain only through the `PerpsChainAPI` interface (see `pkg/client/perps_chain_boundary.go`), which owns the tx encoder and isolates Cosmos/PerpX wire details from higher-level logic and tests.
- **Client ↔ Strategy boundary**: Order generation is delegated to `PerpsOrderStrategy`/`PerpsScenarioConfig` in `pkg/strategies/`, which own scenario presets, market config, and action distributions; the client is responsible for account/sequence management, signing, retries, and tx-status monitoring.
- **Seed ↔ Loadtest boundary**: `pkg/seed/seed.go` derives the same deterministic bench accounts as the bank/perps clients (one per worker ID) and, in perps mode, deposits USDC margin into subaccounts; the loadtest engine treats those accounts as opaque senders and never mutates seeding rules itself.
- **Deterministic randomness**: `PerpsScenarioConfig` and `PerpsOrderStrategy` draw randomness from per-worker RNG streams created by `NewRandForWorker(workerID)`, which in turn derive from `LOADTEST_PERPS_RNG_SEED` when set. This makes whole runs (including order types and parameters) reproducible across CI and local environments.

### Components

1. **Client Factory** (`pkg/client/factory.go`)
   - Creates PerpX bank client instances
   - Assigns unique worker IDs for deterministic account generation

2. **Bank Client** (`pkg/client/bank_client.go`)
   - Implements the `loadtest.Client` interface
   - Generates bank send transactions
   - Manages account sequences and signing

3. **Perps Client** (`pkg/client/perps_client.go`)
   - Implements the `loadtest.Client` interface for perps orders
   - Generates order transactions (place, cancel, amend, close)
   - Tracks worker-local order state for realistic cancel/amend behavior

4. **Seed Module** (`pkg/seed/seed.go`)
   - Generates deterministic test accounts
   - Funds accounts in batches
   - Verifies account balances

5. **Bank Send Strategy** (`pkg/strategies/bank_send.go`)
   - Defines transaction creation logic
   - Configures chain ID, denomination, and sink address

6. **Perps Strategy** (`pkg/strategies/perps_order_strategy.go`, `pkg/strategies/perps_scenarios.go`)
   - Defines perps order creation logic
   - Implements scenario presets (simple_perps, maker_taker, stress_mixed)
   - Configures markets, action weights, and leverage ranges

7. **Load Test Engine** (`pkg/loadtest/`)
   - Core load testing infrastructure from cometbft-load-test
   - Manages connections, transaction generation, and statistics

### Account Generation

Each worker uses a deterministically generated account based on its worker ID:

```go
seedStr := fmt.Sprintf("bench worker %d seed phrase for load testing account", workerID)
```

This ensures:
- **Reproducibility**: Same worker ID always generates the same account
- **Predictability**: Easy to identify which account belongs to which worker
- **Consistency**: Seed command and load test use the same generation logic

### Worker Sharding & Connection Scheduling

Add an explicit **worker-sharding layer** between seeding and the load test engine so that large worker sets are actually exercised during runs.

- **Seeded workers**: The `seed.workers` value in the YAML config (or `--workers` in scripts) determines the total number of bench accounts \(W = WorkersTotal\).
- **Connections and endpoints**: The loadtest config specifies:
  - `connections` per endpoint \(C\)
  - `endpoints` list \(E = len(endpoints)\)
- **Workers per connection**: At config load time (`ConfigFromViper`), the engine derives:
  - `WorkersTotal = seed.workers` (when present in the same YAML file)
  - `WorkersPerConnection = WorkersTotal / (E * C)` (integer division; if this is 0, sharding is disabled)

For each connection, the load test constructs a per-connection view of the config with:

- `EndpointOrdinal` – 0-based index into `endpoints`
- `TransactorIndex` – 0-based connection index within that endpoint

These two fields are used by the client factories to select **which slice of the worker pool each connection owns**:

- **Bank (`perpx-bank`)**:
  - Connection index: `globalConnIndex = EndpointOrdinal * Connections + TransactorIndex`
  - Worker group base: `workerBase = globalConnIndex * WorkersPerConnection`
  - This connection drives senders in `\[workerBase .. workerBase + WorkersPerConnection - 1]`.
  - Within that group, every tick chooses **sender / receiver pairs** using a k‑advance rule so that:
    - For each logical second `t` and per-second index `j`:
      - `k = (2 * Rate * t) mod G` where `G = WorkersPerConnection`
      - `sender = connStart + (k + j) mod G`
      - `receiver = connStart + (k + Rate + j) mod G`
    - With the validation rule `2*Rate <= WorkersPerConnection`, each sender and receiver are distinct and there are no self-sends.
- **Perps (`perpx-perps`)**:
  - When sharding is configured (`WorkersTotal > 0` and `WorkersPerConnection > 0`), the factory builds a `MultiWorkerPerpsClient` per connection.
  - Each `MultiWorkerPerpsClient` owns `G = WorkersPerConnection` underlying perps workers, with base worker ID:
    - `endpointBase = EndpointOrdinal * (WorkersTotal / E)`
    - `connBase = endpointBase + TransactorIndex * WorkersPerConnection`
  - The perps scheduler uses the same k‑advance idea to rotate **signer accounts per tick**:
    - For logical slot `n` (0-based), with `R = Rate` and `G = WorkersPerConnection`:
      - `block = n / R`
      - `offset = n % R`
      - `k = (block * 2 * R) mod G`
      - `workerOffset = (k + offset) mod G`
    - The selected worker index is `connBase + workerOffset`. Over time, this walks the entire worker group per connection.

**Validation guardrails** (in `loadtest.Config.Validate` when `WorkersTotal > 0`):

- Require at least one endpoint and `connections >= 1`.
- Require `E * C <= WorkersTotal` so that each connection has at least one funded worker.
- Derive `workersPerConn = WorkersTotal / (E * C)` and require `workersPerConn > 0`.
- Enforce `2 * Rate <= workersPerConn` so the bank sender/receiver ranges fit without overlap.

### Transaction Flow

1. **Client Generation**: Each worker creates a `PerpxBankClient` instance
2. **Account Initialization**: Client queries account info (account number, sequence) via REST API
3. **Transaction Creation**: Client generates signed bank send transactions
4. **Broadcasting**: Transactions are broadcast via WebSocket to the CometBFT node
5. **Statistics**: Success/failure rates and latency are tracked in real-time

## Configuration Details

### Port Mappings

The tool automatically handles port conversions:

- **RPC**: `36657` (CometBFT RPC) or `26657` (standard)
- **REST API**: `31317` (PerpX) or `1317` (standard)
- **gRPC**: `39090` (PerpX) or `9090` (standard)
- **WebSocket**: `36657/websocket` or `26657/websocket`

### Gas Configuration

- **Gas Limit**: `200,000` per transaction
- **Minimum Gas Price**: `25,000,000,000 aperpx` per unit of gas
- **Fee Calculation**: `gas_limit × min_gas_price`

### Transaction Details

- **Message Type**: `cosmos.bank.v1beta1.MsgSend`
- **Amount**: `1 aperpx` (1 base unit) per transaction
- **Destination**: Configurable sink address (default: faucet address)

## Troubleshooting

### Common Issues

#### "Account does not exist" Error

**Problem**: Load test fails with account not found errors.

**Solution**: Run the seed command first to fund accounts:
```bash
perpx-load-test seed --workers <number-of-workers>
```

#### "Insufficient funds" Error

**Problem**: Seed command fails due to insufficient balance.

**Solution**: Ensure the seed account has enough tokens:
```bash
# Check seed account balance
perpx-load-test seed --help  # Shows seed address

# Fund the seed account or use a different seed key
perpx-load-test seed --seed-key <mnemonic-with-funds>
```

#### "gRPC frame too large" Error

**Problem**: gRPC queries fail with frame size errors.

**Solution**: The tool automatically uses REST API for account queries to avoid this issue. If you encounter this, ensure REST API is accessible on port `31317` or `1317`.

#### Go version mismatch (build fails with "version does not match go tool version")

**Problem**: First build fails with `compile: version "go1.x.x" does not match go tool version "go1.y.y"`.

**Solution**: Use the script's automatic fix (it will clean cache and retry), or run once then use `--skip-build` for later runs. To fix the toolchain once: `go clean -cache && go install -a std`, or install the Go version required by the project (see `go.mod`).

#### "Broken pipe" / "abnormal closure" / "unexpected EOF" with many connections

**Problem**: With a high worker count (e.g. 16,000) to a single RPC node, the run log shows many `write: broken pipe` or `websocket: close 1006 (abnormal closure): unexpected EOF`. The RPC node is closing WebSocket connections.

**Solution**: The CometBFT RPC server has a limit on concurrent connections (and/or resources). Either:
- **Reduce workers** so you stay under the node's limit (e.g. try 1,000–2,000 per endpoint).
- **Raise the node's limit** if you control the node (e.g. `max_open_connections` in config.toml or equivalent).
- **Use multiple endpoints** so connections are spread across nodes: `WS_ENDPOINTS="ws://host1:36657/websocket,ws://host2:36657/websocket"` with fewer workers per endpoint.

#### Connection Timeout

**Problem**: Cannot connect to endpoints.

**Solution**: 
- Verify the PerpX localnet is running
- Check endpoint URLs are correct
- Ensure firewall allows connections
- Try using `http://` instead of `ws://` for RPC endpoints

#### Low Transaction Success Rate

**Problem**: Many transactions fail.

**Possible Causes**:
- Network congestion
- Insufficient gas fees
- Account sequence mismatches
- Network not keeping up with load

**Solution**:
- Reduce transaction rate (`--rate`)
- Increase gas fees (modify in code if needed)
- Check network logs for errors
- Ensure accounts are properly seeded

#### Go Version Mismatch (Benchmark Script)

**Problem**: Build fails with "version does not match go tool version" error.

**Solution**: The script automatically attempts to fix this by cleaning caches and rebuilding. If automatic fix fails:
```bash
# Manual fix
go clean -cache -modcache -testcache
go install -a std

# Or use existing binary
./scripts/run_benchmark.sh --skip-build
```

#### Benchmark Script Can't Find Go

**Problem**: Script reports "Cannot find Go binary".

**Solution**: Ensure Go is installed and in PATH, or install it:
```bash
# Check if Go is installed
which go

# Install Go if needed (see https://go.dev/doc/install)
```

## Development

### Project Structure

```
perpx-load-test/
├── cmd/
│   └── perpx-load-test/
│       └── main.go              # Entry point
├── pkg/
│   ├── client/
│   │   ├── bank_client.go       # PerpX bank client implementation
│   │   └── factory.go           # Client factory
│   ├── loadtest/                # Core load test engine (from cometbft-load-test)
│   ├── seed/
│   │   └── seed.go              # Account seeding logic
│   └── strategies/
│       └── bank_send.go         # Bank send transaction strategy
├── internal/
│   └── logging/                 # Logging utilities
├── scripts/
│   └── run_benchmark.sh         # Automated benchmark script
├── go.mod                       # Go module definition
└── README.md                    # This file
```

### Building

```bash
# Build binary
go build -o perpx-load-test ./cmd/perpx-load-test

# Build with version info
go build -ldflags "-X github.com/1119-Labs/perpx-load-test/pkg/loadtest.cliVersionCommitID=$(git rev-parse HEAD)" \
  -o perpx-load-test ./cmd/perpx-load-test
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run specific package tests
go test ./pkg/seed
go test ./pkg/client
```

### Adding New Client Types

To add a new transaction type:

1. Create a new strategy in `pkg/strategies/`
2. Implement the `loadtest.Client` interface in `pkg/client/`
3. Create a factory implementing `loadtest.ClientFactory`
4. Register the factory in `main.go`

Example:
```go
// In main.go
loadtest.RegisterClientFactory("my-client", client.NewMyClientFactory())
```

**Reference Implementation**: See `pkg/client/perps_client.go` and `pkg/client/perps_factory.go` for a complete example of a non-bank client that:
- Implements deterministic account generation
- Manages worker-local state (order tracking)
- Generates diverse message types based on scenario configuration
- Handles REST API account queries
- Signs transactions with proper sequence management

For detailed architecture information, see the [Architecture Documentation](docs/architecture.md).

## Performance Tips

1. **Connection Count**: More connections can increase throughput, but too many may overwhelm the network
2. **Transaction Rate**: Start with lower rates and gradually increase
3. **Batch Size**: Larger batch sizes in seed command reduce transaction count but increase per-transaction size
4. **Network Topology**: Test on the same network as the blockchain for best results
5. **Resource Monitoring**: Monitor CPU, memory, and network usage during tests

## Limitations

- Designed primarily for localnet testing
- Account generation is deterministic but not cryptographically secure (for testing only)
- Default sink address for bank sends is hardcoded (can be overridden via environment variable)
- Perps scenarios use approximate order tracking (client-side only, not reconciled with on-chain state)
- Perps order scenarios require markets to be configured via `LOADTEST_PERPS_MARKETS` (defaults to a single generic market)

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## Related Projects

- [PerpX Protocol](https://github.com/1119-Labs/perpx-chain) - The PerpX blockchain protocol
- [CometBFT Load Test](https://github.com/cometbft/cometbft-load-test) - Base load testing framework
- [CometBFT](https://github.com/cometbft/cometbft) - Byzantine Fault Tolerant consensus engine

## Support

For issues, questions, or contributions, please open an issue on the GitHub repository.

---

**Note**: This tool is designed for development and testing purposes. Do not use the default seed keys or test accounts in production environments.

