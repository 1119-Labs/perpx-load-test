# Config documentation

This directory contains example YAML configs for `perpx-load-test`.

- `localnet/`: intended for the standard localnet stack (`perpx-chain/deployment/localnet`)

## How config is loaded (precedence)

### Load testing (`perpx-load-test ...`)

When you run the main CLI with `--config path/to/file.yaml`, values are resolved with this precedence:

**CLI flags > YAML (`loadtest.*`) > env vars > defaults**

Notes:
- The root flags map onto the `loadtest.*` section (e.g. `--rate` overrides `loadtest.rate`).
- `loadtest.endpoints` can also be provided via `--endpoints`, `WS_ENDPOINTS` (comma-separated), or `WS_URL` (single endpoint), depending on how you run.

### Seeding (`perpx-load-test seed ...`)

When you run seeding with `--config path/to/file.yaml`, values are resolved with this precedence:

**seed flags > env vars > YAML (`seed.*`) > defaults**

One exception:
- `seed.restUrl` may be derived from `loadtest.restUrl` when using `--config` (so a single YAML can define REST once under `loadtest.*` and both loadtest + seed will use it).

## YAML schema

All example configs share a common top-level structure:

```yaml
version: "1"

seed:
  ...

loadtest:
  ...
```

### `version`

- **`version`**
  - **Description**: config format version for humans/tools.
  - **Default**: none (optional; currently examples use `"1"`).
  - **Notes**: the Go code does not currently enforce this field.

### `seed.*` (account seeding)

Seeding prepares funded accounts for the load test.

- **`seed.workers`**
  - **Description**: number of accounts to seed.
  - **Default**: `10` (seed CLI flag default).
  - **Notes**: used to derive worker sharding for loadtest if present in the same YAML.
- **`seed.seedKey`**
  - **Description**: key name or mnemonic used to derive the seeding account.
  - **Default**: `""` (must be provided via YAML/flag/env unless using `seedPrivateKey`).
  - **Notes**: if `seed.seedPrivateKey` is set, it takes precedence.
- **`seed.seedPrivateKey`**
  - **Description**: hex-encoded private key for the seeding account.
  - **Default**: `""` (unset).
  - **Notes**: if set, overrides `seed.seedKey`.
- **`seed.rpc`**
  - **Description**: CometBFT RPC endpoint (HTTP) used for chain interaction.
  - **Default**: `http://localhost:36657`.
- **`seed.restUrl`**
  - **Description**: REST base URL for balance/account queries.
  - **Default**: `""` (when unset, seed logic may fall back to using `seed.rpc` for REST calls).
  - **Notes**: when using `--config`, this can be derived from `loadtest.restUrl` (see precedence notes above).
- **`seed.grpc`**
  - **Description**: gRPC endpoint used for broadcasting seed transactions.
  - **Default**: `""` (unset; code may derive or use env override).
  - **Env override**: `LOADTEST_GRPC_URL`.
- **`seed.chainId`**
  - **Description**: chain ID used for signing.
  - **Default**: `localperpxprotocol`.
- **`seed.denom`**
  - **Description**: base denom used for bank funding.
  - **Default**: `aperpx`.
- **`seed.fundAmount`**
  - **Description**: amount to fund each seeded account (coin string, e.g. `"1000aperpx"`).
  - **Default**: `"1000000000000000000aperpx"`.
- **`seed.batchSize`**
  - **Description**: number of accounts funded per funding transaction.
  - **Default**: `50`.
- **`seed.mode`**
  - **Description**: seeding mode.
  - **Allowed values**: `bank` (fund base token only) or `perps` (also deposit perps margin).
  - **Default**: `bank`.

Perps-mode seeding additionally deposits margin (USDC quantums) to each worker’s subaccount 0:

- **`seed.perpsDeposit`**
  - **Description**: per-account margin deposit amount in USDC *quantums* (integer string).
  - **Default**: `"10000000000"` (10,000 USDC if 1 USDC = 1e6 quantums).
  - **Notes**: only used when `seed.mode: perps`.
- **`seed.perpsDepositDenom`**
  - **Description**: denom label for perps deposits (quantums).
  - **Default**: `usdc`.
  - **Notes**: only used when `seed.mode: perps`.

### `loadtest.*` (standalone load testing)

- **`loadtest.clientFactory`**
  - **Description**: which client factory to use (e.g. `perpx-bank`, `perpx-perps`).
  - **Default**: `perpx-bank`.
- **`loadtest.connections`**
  - **Description**: websocket connections per endpoint.
  - **Default**: `1`.
- **`loadtest.time`**
  - **Description**: run duration in seconds.
  - **Default**: `60`.
- **`loadtest.sendPeriod`**
  - **Description**: send interval in seconds (how often each connection emits a batch).
  - **Default**: `1`.
- **`loadtest.rate`**
  - **Description**: number of txs generated per `sendPeriod` per connection (per endpoint).
  - **Default**: `1000`.
  - **Notes**: worker sharding validation requires \(2 * rate \le \text{workersPerConnection}\) when `seed.workers` is present.
- **`loadtest.size`**
  - **Description**: target transaction size in bytes (payload sizing depends on factory/strategy).
  - **Default**: `250`.
- **`loadtest.count`**
  - **Description**: max txs to send (global cap for the run).
  - **Default**: `-1` (unlimited).
- **`loadtest.broadcastTxMethod`**
  - **Description**: CometBFT `broadcast_tx_*` method used to submit transactions.
  - **Allowed values**: `async`, `sync`, `commit`.
  - **Default**: `async`.
- **`loadtest.endpoints`**
  - **Description**: list of CometBFT WebSockets RPC endpoints to connect to.
  - **Default**: none.
  - **Required**: yes (config validation fails if empty).
  - **Other ways to set**:
    - `--endpoints` (CLI)
    - `WS_ENDPOINTS` env (comma-separated)
    - `WS_URL` env (single endpoint)
- **`loadtest.restUrl`**
  - **Description**: REST base URL for account/sequence queries and optional tx status checks (e.g. `http://localhost:31317`).
  - **Default**: `""` (unset).
  - **Notes**: clients may derive this from endpoints or rely on env config depending on factory.
- **`loadtest.chainId`**
  - **Description**: chain ID used for signing/client context.
  - **Default**: `localperpxprotocol`.
- **`loadtest.denom`**
  - **Description**: fee denom / chain denom used by clients.
  - **Default**: `aperpx`.
- **`loadtest.ui`**
  - **Description**: UI mode for standalone execution.
  - **Allowed values**: `plain`, `tui`.
  - **Default**: `plain`.
- **`loadtest.statsOutputFile`**
  - **Description**: path to write final aggregate stats CSV.
  - **Default**: `""` (don’t write).

#### `loadtest.debug.*`

- **`loadtest.debug.logWorkerIDs`**
  - **Description**: include worker IDs in per-tx logs (useful for debugging worker sharding / scheduling).
  - **Default**: `false`.

#### Optional: endpoint selection / peer connectivity knobs

- **`loadtest.endpointSelectMethod`**
  - **Description**: how to choose endpoints when peer discovery/crawling is enabled.
  - **Allowed values**: `supplied`, `discovered`, `any`.
  - **Default**: `supplied`.
- **`loadtest.expectPeers`**
  - **Description**: minimum number of peers to discover/expect before starting.
  - **Default**: `0` (no requirement).
- **`loadtest.maxEndpoints`**
  - **Description**: cap on number of endpoints to use for testing.
  - **Default**: `0` (unlimited).
- **`loadtest.minConnectivity`**
  - **Description**: minimum peer connectivity requirement (per peer) before starting.
  - **Default**: `0` (no requirement).
- **`loadtest.peerConnectTimeout`**
  - **Description**: max seconds to wait for peer connectivity requirements when `expectPeers > 0`.
  - **Default**: `600`.

#### `loadtest.perps.*` (perps scenario + client tuning)

Used when `loadtest.clientFactory: perpx-perps`:

- **`loadtest.perps.scenario`**
  - **Description**: scenario preset name used to generate perps behavior.
  - **Default**: `simple_perps`.
- **`loadtest.perps.markets`**
  - **Description**: market list / sizing string (same format as `LOADTEST_PERPS_MARKETS`).
  - **Default**: `""` (scenario defaults may apply; many scenarios expect this to be set).
- **`loadtest.perps.actionWeights`**
  - **Description**: weights for actions in order `"place,cancel,amend,close,noop"`.
  - **Default**: `""` (scenario defaults may apply).
- **`loadtest.perps.maxTrackedOrders`**
  - **Description**: cap on number of open orders tracked client-side.
  - **Default**: `0` (interpreted as “use strategy default”).
- **`loadtest.perps.minLeverage`**
  - **Description**: minimum leverage (kept as string to avoid YAML float quirks).
  - **Default**: `""` (interpreted as “use strategy default”).
- **`loadtest.perps.maxLeverage`**
  - **Description**: maximum leverage (string).
  - **Default**: `""` (interpreted as “use strategy default”).
- **`loadtest.perps.useMidPrice`**
  - **Description**: if true, use mid price when generating orders (strategy-dependent).
  - **Default**: unset/false.
  - **Notes**: currently only a `true` value is latched into the config (because it’s represented as an optional pointer). If you need “explicit false” behavior, omit the key.
- **`loadtest.perps.useTimestampNonce`**
  - **Description**: use timestamp nonces to reduce account-sequence contention at high concurrency.
  - **Default**: `false`.
- **`loadtest.perps.monitorTxStatus`**
  - **Description**: enable tx status polling/monitoring.
  - **Default**: `false`.
- **`loadtest.perps.txMonitorSampleRate`**
  - **Description**: sampling rate for tx monitoring (0 disables).
  - **Default**: `0`.
- **`loadtest.perps.maxRetries`**
  - **Description**: max retries for perps tx submission/handling (strategy-dependent).
  - **Default**: `0` (interpreted as “use strategy default”).
- **`loadtest.perps.retryDelayMs`**
  - **Description**: delay between retries in milliseconds.
  - **Default**: `0` (interpreted as “use strategy default”).
- **`loadtest.perps.enableMarginCheck`**
  - **Description**: enable margin checking before placing orders.
  - **Default**: `false`.
- **`loadtest.perps.minMarginRatio`**
  - **Description**: minimum margin ratio threshold (string).
  - **Default**: `""` (interpreted as “use strategy default”).
- **`loadtest.perps.onInsufficientMargin`**
  - **Description**: what to do when margin is insufficient.
  - **Allowed values**: `skip`, `deposit`, `close`.
  - **Default**: `""` (interpreted as “use strategy default”).
- **`loadtest.perps.feeGasLimit`**
  - **Description**: gas limit for perps tx building.
  - **Default**: `0` (interpreted as “use strategy default”).
- **`loadtest.perps.feeMinGasPrice`**
  - **Description**: min gas price in base denom units per gas (string).
  - **Default**: `""` (interpreted as “use strategy default”).
- **`loadtest.perps.feeAltDenom`**
  - **Description**: alternate fee denom (e.g. IBC USDC denom).
  - **Default**: `""` (interpreted as “use strategy default”).
- **`loadtest.perps.feeAltMinGasPrice`**
  - **Description**: min gas price in alternate denom units per gas (string).
  - **Default**: `""` (interpreted as “use strategy default”).

Additional optional knobs supported by the code (often left unset in YAML so defaults apply):

- **`loadtest.perps.priceOffsetBps`**
  - **Description**: price offset in basis points (strategy-dependent).
  - **Default**: `0`.
- **`loadtest.perps.marketDataUrl`**
  - **Description**: optional external market data URL.
  - **Default**: `""`.
- **`loadtest.perps.marketDataCacheTTLSeconds`**
  - **Description**: TTL for market data cache.
  - **Default**: `0`.
- **`loadtest.perps.batchCancelPct`**, **`loadtest.perps.longTermPct`**, **`loadtest.perps.conditionalPct`**
  - **Description**: scenario mix knobs (percent allocations).
  - **Default**: `0`.
- **`loadtest.perps.twapPct`**, **`loadtest.perps.twapIntervalSeconds`**, **`loadtest.perps.twapNumIntervals`**
  - **Description**: TWAP order mix and parameters.
  - **Default**: `0`.
- **`loadtest.perps.deterministicClientIds`**
  - **Description**: if true, derive deterministic client IDs.
  - **Default**: `false`.
- **`loadtest.perps.clientIdStart`**
  - **Description**: starting client ID when deterministic IDs are enabled.
  - **Default**: `0`.
- **`loadtest.perps.txCheckDelayMs`**
  - **Description**: delay before first tx status check (ms).
  - **Default**: `0`.
- **`loadtest.perps.txMaxChecks`**
  - **Description**: max number of tx status checks.
  - **Default**: `0`.
- **`loadtest.perps.subticksPerTick`**
  - **Description**: chain sampling knob; 0 means “use chain/client default”.
  - **Default**: `0`.
- **`loadtest.perps.stepBaseQuantums`**
  - **Description**: chain sampling knob; 0 means “use chain/client default”.
  - **Default**: `0`.
- **`loadtest.perps.rngSeed`**
  - **Description**: base RNG seed (0 uses time-based seed).
  - **Default**: `0`.

## Example configs

- Bank-focused localnet: `config/localnet/config-bank.yaml`
- Perps-focused localnet: `config/localnet/config-perps.yaml`

