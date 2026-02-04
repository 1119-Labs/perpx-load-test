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
go build -o perpx-load-test ./cmd/perpx-load-test

# Or install globally
go install ./cmd/perpx-load-test
```

## Quick Start

### Option 1: Automated Benchmark Script (Recommended)

The easiest way to run benchmarks is using the automated script that handles both seeding and load testing:

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
# Basic usage (uses default "alice" validator key)
perpx-load-test seed --workers 10

# With custom configuration
perpx-load-test seed \
  --workers 50 \
  --rpc http://localhost:36657 \
  --chain-id localperpxprotocol \
  --fund-amount 1000000aperpx \
  --batch-size 50
```

The seed command will:
- Generate deterministic accounts for each worker
- Check which accounts need funding
- Fund accounts in batches via multi-message transactions
- Verify all accounts are properly funded

#### 2. Run Load Test

Once accounts are seeded, run the load test:

```bash
# Basic load test (1 connection, 1000 tx/s, 60 seconds)
perpx-load-test \
  --connections 1 \
  --rate 1000 \
  --time 60 \
  ws://localhost:36657/websocket

# High-throughput test (10 connections, 5000 tx/s, 120 seconds)
perpx-load-test \
  --connections 10 \
  --rate 5000 \
  --time 120 \
  ws://localhost:36657/websocket

# With TUI (Terminal User Interface) for real-time stats
perpx-load-test \
  --connections 5 \
  --rate 2000 \
  --time 60 \
  --ui tui \
  ws://localhost:36657/websocket
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
| `--seed-key` | `-k` | Key name or mnemonic for seeding | `alice` |
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
| `SEED_KEY` | Seed key/mnemonic | `alice` |
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
| `--seed-key` | `-k` | Key name or mnemonic for seeding | `alice` |
| `--seed-private-key` | `-p` | Hex-encoded private key (takes precedence) | - |
| `--rpc` | `-r` | RPC endpoint | `http://localhost:36657` |
| `--chain-id` | | Chain ID | `localperpxprotocol` |
| `--denom` | | Token denomination | `aperpx` |
| `--fund-amount` | | Amount to fund each account | `1000000aperpx` |
| `--batch-size` | | Accounts per transaction | `50` |
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
| `--client-factory` | | Client factory identifier | `perpx-bank` |
| `--connections` | `-c` | Connections per endpoint | `1` |
| `--time` | `-T` | Test duration (seconds) | `60` |
| `--send-period` | `-p` | Send period (seconds) | `1` |
| `--rate` | `-r` | Transactions per second | `1000` |
| `--size` | `-s` | Transaction size (bytes) | `250` |
| `--count` | | Max transactions to send | `-1` (unlimited) |
| `--broadcast-tx-method` | | Broadcast method (`sync`, `async`, `commit`) | `sync` |
| `--ui` | | UI mode (`tui`, `none`) | `none` |
| `--verbose` | | Enable verbose logging | `false` |

#### Examples

```bash
# Quick 30-second test
perpx-load-test --time 30 --rate 500 ws://localhost:36657/websocket

# High-throughput test with multiple connections
perpx-load-test \
  --connections 10 \
  --rate 10000 \
  --time 300 \
  ws://localhost:36657/websocket

# Test with TUI for real-time monitoring
perpx-load-test \
  --connections 5 \
  --rate 2000 \
  --time 120 \
  --ui tui \
  ws://localhost:36657/websocket

# Limit total transaction count
perpx-load-test \
  --count 10000 \
  --rate 1000 \
  ws://localhost:36657/websocket
```

## Environment Variables

You can configure the tool using environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `LOADTEST_SEED_KEY` | Seed key/mnemonic for seeding | `alice` |
| `LOADTEST_SEED_PRIVATE_KEY` | Hex-encoded private key for seeding | - |
| `LOADTEST_RPC` | RPC endpoint | `http://localhost:36657` |
| `LOADTEST_CHAIN_ID` | Chain ID | `localperpxprotocol` |
| `LOADTEST_DENOM` | Token denomination | `aperpx` |
| `LOADTEST_FUND_AMOUNT` | Amount to fund each account | `1000000aperpx` |
| `LOADTEST_SINK_ADDRESS` | Destination address for bank sends | `perpx1kyfmupa8z5jtxgf5f4gt285sepeg6eqnzvs25m` |

## Architecture

### Components

1. **Client Factory** (`pkg/client/factory.go`)
   - Creates PerpX bank client instances
   - Assigns unique worker IDs for deterministic account generation

2. **Bank Client** (`pkg/client/bank_client.go`)
   - Implements the `loadtest.Client` interface
   - Generates bank send transactions
   - Manages account sequences and signing

3. **Seed Module** (`pkg/seed/seed.go`)
   - Generates deterministic test accounts
   - Funds accounts in batches
   - Verifies account balances

4. **Bank Send Strategy** (`pkg/strategies/bank_send.go`)
   - Defines transaction creation logic
   - Configures chain ID, denomination, and sink address

5. **Load Test Engine** (`pkg/loadtest/`)
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

## Performance Tips

1. **Connection Count**: More connections can increase throughput, but too many may overwhelm the network
2. **Transaction Rate**: Start with lower rates and gradually increase
3. **Batch Size**: Larger batch sizes in seed command reduce transaction count but increase per-transaction size
4. **Network Topology**: Test on the same network as the blockchain for best results
5. **Resource Monitoring**: Monitor CPU, memory, and network usage during tests

## Limitations

- Currently supports only bank send transactions
- Designed primarily for localnet testing
- Account generation is deterministic but not cryptographically secure (for testing only)
- Default sink address is hardcoded (can be overridden via environment variable)

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

