# jtron-oracle

`jtron-oracle` is the java-tron half of go-tron's differential TVM harness. It executes a
fully specified in-memory world through java-tron's real `Program` and `VM`, then emits the
same normalized `Execution` JSON as `internal/vmoracle`.

The source lives in go-tron, while the Gradle init script compiles it as an additional
java-tron `framework` source directory. This keeps the pinned java-tron checkout unmodified.

## Requirements

- macOS ARM64: JDK 17 (verified with Temurin 17.0.19)
- a local java-tron checkout pinned to `GreatVoyage-v4.8.1.1`
- by default the checkout is expected at `../java-tron`; override with `JAVA_TRON_ROOT`

## Protocol

The process reads newline-delimited JSON from stdin and writes exactly one JSON object per
line to stdout. Diagnostics go to stderr.

Health check:

```json
{"method":"ping"}
```

Execution request:

```json
{"world":{"version":23,"dynamicProps":{"energyFee":100},"block":{},"accounts":{}},"tx":{"type":"TriggerSmartContract"}}
```

The complete schema is defined by `internal/vmoracle.Request`, `World`, `Tx`, and
`Execution`. Hex strings are lowercase and do not include `0x`.

## Run

```bash
tools/jtron-oracle/run.sh
```

The launcher builds only the required java-tron classes, writes a cached runtime classpath,
and then replaces itself with the persistent oracle JVM. Gradle output is sent to stderr so
stdout remains valid NDJSON.

## Mainnet runtime corpus

`test/differential/testdata/mainnet_contracts.json` is an offline snapshot captured from
TronGrid's `wallet/getcontractinfo` endpoint. It contains deployed runtime bytecode, the raw
ABI, and deterministic ABI calldata for USDT plus the USDC implementation/proxy pair. Validate
the fixture without a JVM, or run every vector through both VMs, with:

```bash
go test -run TestMainnetContractCorpusFixture ./internal/vmoracle
make oracle-mainnet
```

Refresh the snapshot (including provenance timestamp, runtime code hash, ABI, and calldata) with
`make capture-mainnet-corpus`. The checked-in fixture keeps the differential suite offline and
reproducible; network access is needed only for an intentional refresh.

## Determinism contract and v0 boundary

- java-tron is pinned to `GreatVoyage-v4.8.1.1`.
- requests are processed serially by one JVM.
- wall-clock and CPU timeout checks are disabled through java-tron's debug setting.
- block number, timestamp, witness, fork version, bytecode, calldata, balances, and storage
  are request inputs.
- every request receives a fresh in-memory repository; no state crosses request boundaries.
- v0 supports `TriggerSmartContract` and reports VM result, return bytes, VM energy, net
  storage writes, and logs. Receipt fee fields use the no-stake formula; java-tron's full
  resource billing path and create transactions are the next vertical slice.
