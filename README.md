# go-tron

A **Go implementation of a TRON full node**, protocol-compatible with the canonical Java node ([`tronprotocol/java-tron`](https://github.com/tronprotocol/java-tron)) — a client-diversity effort for the TRON network.

> **Status:** early design / scaffold. Not yet a working node.

## What this is (and isn't)

- **Is:** a second TRON node implementation whose north star is *bit-for-bit consensus equivalence* with java-tron — same DPoS, same TVM execution & energy metering, same state roots — verified by differential replay against mainnet.
- **Isn't:** a client SDK. For signing/constructing/querying from Go, use [`fbsobreira/gotron-sdk`](https://github.com/fbsobreira/gotron-sdk). go-tron *reuses* that SDK's proto/address/crypto primitives but rebuilds execution, consensus, storage and P2P.

## Why Go

`go-libp2p` is first-class (java-tron uses a JVM port), goroutines fit the sync/production pipelines, a single static binary simplifies ops, and Pebble gives a pure-Go storage engine (no cgo).

## Layout

```
cmd/gotron      node entrypoint
cmd/toolkit     DB/keystore maintenance
internal/       types, crypto, address, db, state, actuator, tvm, consensus, node, p2p, api, ...
test/           differential replay harness + vectors
```

## License

TBD.
