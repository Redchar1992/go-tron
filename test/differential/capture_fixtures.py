#!/usr/bin/env python3
"""Capture real TRON mainnet blocks from TronGrid into differential-replay fixtures.

The replay harness (replay_test.go) is the M2 oracle: it reconstructs each block from
these fixtures and asserts its recomputed block id + txTrieRoot equal the on-chain
values. We commit the fixtures so the test runs offline; re-run this only to refresh.

Outputs (test/differential/testdata/):
  chain.json  - a contiguous run from genesis (block 0..CHAIN_END), replayed through the
                Manager: proves block-id over real headers, empty-block ZERO txTrieRoot,
                parent linkage, and the linear pipeline.
  spot.json   - individual transaction-bearing blocks, verified for ROOT equivalence only
                (multi-tx Merkle over real TransferContract/VoteWitnessContract bytes).

Usage:  python3 capture_fixtures.py
"""

import json
import os
import subprocess

BASE = "https://api.trongrid.io/wallet"
CHAIN_END = 20            # contiguous blocks 0..CHAIN_END
SPOT_BLOCKS = [1000000, 2000000]  # tx-bearing blocks (no smart contracts in these)
DENSE_START, DENSE_END = 3000000, 3000011  # contiguous dense pre-TVM span
TESTDATA = os.path.join(os.path.dirname(__file__), "testdata")


def call(endpoint, payload):
    # Shell out to curl: this machine's Python SSL trust store is incomplete.
    out = subprocess.check_output([
        "curl", "-sS", "--max-time", "20",
        "-X", "POST", f"{BASE}/{endpoint}",
        "-H", "Content-Type: application/json",
        "-d", json.dumps(payload),
    ])
    return json.loads(out)


def fetch(num):
    return call("getblockbynum", {"num": num})


def to_fixture(b):
    h = b["block_header"]["raw_data"]
    txs = []
    for tx in b.get("transactions", []):
        txs.append({
            "txID": tx["txID"],
            "rawDataHex": tx["raw_data_hex"],
            "signatures": tx.get("signature", []),
        })
    return {
        "number": h.get("number", 0),
        "blockID": b["blockID"],
        "timestamp": h.get("timestamp", 0),
        "parentHash": h.get("parentHash", ""),
        "txTrieRoot": h.get("txTrieRoot", ""),
        "witnessAddress": h.get("witness_address", ""),
        "witnessId": h.get("witness_id", 0),
        "version": h.get("version", 0),
        "accountStateRoot": h.get("accountStateRoot", ""),
        "transactions": txs,
    }


def main():
    os.makedirs(TESTDATA, exist_ok=True)

    chain = [to_fixture(fetch(n)) for n in range(0, CHAIN_END + 1)]
    with open(os.path.join(TESTDATA, "chain.json"), "w") as f:
        json.dump({"blocks": chain}, f, indent=2)
    print(f"wrote chain.json: blocks 0..{CHAIN_END} "
          f"({sum(len(b['transactions']) for b in chain)} txs total)")

    spot = [to_fixture(fetch(n)) for n in SPOT_BLOCKS]
    with open(os.path.join(TESTDATA, "spot.json"), "w") as f:
        json.dump({"blocks": spot}, f, indent=2)
    print("wrote spot.json:", ", ".join(
        f"#{b['number']} ({len(b['transactions'])} txs)" for b in spot))

    # Dense contiguous pre-TVM span + per-transaction receipts (the fee/bandwidth oracle).
    dense = [to_fixture(fetch(n)) for n in range(DENSE_START, DENSE_END + 1)]
    with open(os.path.join(TESTDATA, "dense.json"), "w") as f:
        json.dump({"blocks": dense}, f, indent=2)
    ntx = sum(len(b["transactions"]) for b in dense)
    print(f"wrote dense.json: blocks {DENSE_START}..{DENSE_END} ({ntx} txs)")

    receipts = {}
    for b in dense:
        for tx in b["transactions"]:
            info = call("gettransactioninfobyid", {"value": tx["txID"]})
            r = info.get("receipt", {})
            receipts[tx["txID"]] = {
                "fee": info.get("fee", 0),
                "netUsage": r.get("net_usage", 0),
                "netFee": r.get("net_fee", 0),
                # Energy fields drive the M3.5 energy-receipt oracle (contract txs).
                "energyUsage": r.get("energy_usage", 0),
                "energyFee": r.get("energy_fee", 0),
                "originEnergyUsage": r.get("origin_energy_usage", 0),
                "energyUsageTotal": r.get("energy_usage_total", 0),
            }
    with open(os.path.join(TESTDATA, "receipts.json"), "w") as f:
        json.dump(receipts, f, indent=2)
    print(f"wrote receipts.json: {len(receipts)} tx receipts")


if __name__ == "__main__":
    main()
