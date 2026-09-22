#!/usr/bin/env python3
"""Capture a reproducible latest-state USDT call fixture from TRON mainnet.

This is the first live-state vertical slice for the M3.5e sign-off.  TronGrid's JSON-RPC
endpoint currently exposes ``latest`` (not historical) storage, so the fixture records the
exact block tag/number, deployed runtime, the storage words needed by the selected ABI calls,
and the ``eth_call`` return bytes.  The committed snapshot is replayed offline by
``TestMainnetLiveUSDTFixture``; refreshing it intentionally requires network access.
"""

import hashlib
import json
import os
import subprocess
from datetime import datetime, timezone

from Crypto.Hash import keccak


RPC_ENDPOINT = "https://api.trongrid.io/jsonrpc"
CORPUS = os.path.join(os.path.dirname(__file__), "testdata", "mainnet_contracts.json")
OUTPUT = os.path.join(os.path.dirname(__file__), "testdata", "mainnet_live_usdt.json")
CHUNK_SIZE = 80


def rpc_batch(requests):
    """Send a bounded JSON-RPC batch through curl (Python SSL stores vary by host)."""
    raw = subprocess.check_output([
        "curl", "-sS", "--max-time", "60", "-X", "POST", RPC_ENDPOINT,
        "-H", "Content-Type: application/json", "-d", json.dumps(requests),
    ])
    responses = json.loads(raw)
    if isinstance(responses, dict):
        responses = [responses]
    by_id = {item["id"]: item for item in responses}
    errors = [item for item in responses if "error" in item]
    if errors:
        raise RuntimeError(f"JSON-RPC batch failed: {errors[:2]}")
    return by_id


def rpc_call(method, params):
    return rpc_batch([{
        "jsonrpc": "2.0", "id": 1, "method": method, "params": params,
    }])[1]["result"]


def keccak_hex(value):
    digest = keccak.new(digest_bits=256)
    digest.update(value)
    return digest.hexdigest()


def abi_address_word(address_hex):
    if not address_hex.startswith("41") or len(address_hex) != 42:
        raise ValueError(f"invalid TRON address: {address_hex}")
    return bytes.fromhex("00" * 12 + address_hex[2:])


def storage_keys(addresses):
    """Cover direct slots and Solidity mapping layouts used by the selected ERC-20 getters."""
    keys = {f"{index:064x}" for index in range(32)}
    for owner in addresses:
        for slot in range(32):
            inner = keccak_hex(abi_address_word(owner) + slot.to_bytes(32, "big"))
            keys.add(inner)
            for spender in addresses:
                # Solidity mapping(address => mapping(address => uint256)); keep both
                # concatenation orders so the fixture remains useful for legacy layouts.
                keys.add(keccak_hex(bytes.fromhex(inner) + abi_address_word(spender)))
                keys.add(keccak_hex(abi_address_word(spender) + bytes.fromhex(inner)))
    return sorted(keys)


def fetch_storage(contract, keys):
    values = {}
    requests = []
    for index, key in enumerate(keys):
        requests.append({
            "jsonrpc": "2.0", "id": index, "method": "eth_getStorageAt",
            "params": [f"0x{contract}", f"0x{key}", "latest"],
        })
    for offset in range(0, len(requests), CHUNK_SIZE):
        responses = rpc_batch(requests[offset:offset + CHUNK_SIZE])
        for index, request in enumerate(requests[offset:offset + CHUNK_SIZE]):
            value = responses[request["id"]]["result"].removeprefix("0x").lower()
            if int(value, 16) != 0:
                values[request["params"][1].removeprefix("0x").zfill(64)] = value.zfill(64)
    return values


def main():
    with open(CORPUS) as source:
        corpus = json.load(source)
    contract = next(item for item in corpus["contracts"] if item["name"].startswith("USDT"))
    address_hex = contract["addressHex"]
    evm_address = address_hex[2:]
    address_map = {item["address"]: item["addressHex"] for item in corpus["contracts"]}

    # Keep calls that do not mutate state. They cover dynamic strings, scalar storage, mappings,
    # blacklist status, and arithmetic over a real high-traffic mainnet token.
    calls = []
    for call in contract["calldata"]:
        name = call["method"].split("(", 1)[0]
        entry = next(item for item in contract["abi"]["entrys"]
                     if item.get("type", "").lower() == "function"
                     and item.get("name") == name
                     and len(item.get("inputs", [])) == len(call["args"]))
        if entry.get("stateMutability", "").lower() not in ("view", "pure"):
            continue
        calls.append(call)

    from_hex = "410000000000000000000000000000000000000001"
    addresses = []
    for call in calls:
        for argument in call["args"]:
            if argument in address_map and address_map[argument] not in addresses:
                addresses.append(address_map[argument])

    code = rpc_call("eth_getCode", [f"0x{evm_address}", "latest"]).removeprefix("0x").lower()
    if not code or code == "0x":
        raise RuntimeError("mainnet returned empty USDT runtime code")
    storage = fetch_storage(evm_address, storage_keys(addresses))

    requests = []
    for index, call in enumerate(calls):
        requests.append({
            "jsonrpc": "2.0", "id": index, "method": "eth_call",
            "params": [{
                "to": f"0x{evm_address}",
                "from": f"0x{from_hex[2:]}",
                "data": f"0x{call['data']}",
            }, "latest"],
        })
    call_responses = rpc_batch(requests)
    captured_calls = []
    for index, call in enumerate(calls):
        result = call_responses[index]["result"].removeprefix("0x").lower()
        captured_calls.append({
            "method": call["method"],
            "args": call["args"],
            "data": call["data"],
            "fromHex": from_hex,
            "return": result,
        })

    block_number = int(rpc_call("eth_blockNumber", []), 16)
    block_hash = rpc_call("eth_getBlockByNumber", ["latest", False])["hash"].removeprefix("0x")
    fixture = {
        "schemaVersion": 1,
        "network": "mainnet",
        "capturedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "source": {
            "endpoint": RPC_ENDPOINT,
            "blockTag": "latest",
            "blockNumber": block_number,
            "blockHash": block_hash,
            "note": "eth_getStorageAt and eth_call are latest-state reads; refresh for a new snapshot",
        },
        "contract": {
            "name": contract["name"],
            "address": contract["address"],
            "addressHex": address_hex,
            "runtimeBytecode": code,
            "codeHash": hashlib.sha256(bytes.fromhex(code)).hexdigest(),
            "storage": storage,
        },
        "calls": captured_calls,
    }
    os.makedirs(os.path.dirname(OUTPUT), exist_ok=True)
    with open(OUTPUT, "w") as destination:
        json.dump(fixture, destination, indent=2)
        destination.write("\n")
    print(f"wrote {OUTPUT}: block {block_number}, {len(storage)} non-zero slots, {len(captured_calls)} calls")


if __name__ == "__main__":
    main()
