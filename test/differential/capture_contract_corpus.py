#!/usr/bin/env python3
"""Capture verified mainnet runtime code, ABI, and deterministic calldata vectors.

The resulting fixture is committed so differential tests are reproducible offline.  Refresh it
when a contract is upgraded or when the corpus is intentionally expanded:

    python3 test/differential/capture_contract_corpus.py

The endpoint returns both init bytecode and ``runtimecode``.  Only the latter is stored because
the oracle executes TriggerSmartContract calls against already-deployed code.  ABI calldata is
encoded with the TRON/EVM convention (the 0x41 address prefix is omitted inside a 32-byte ABI
address word).
"""

import hashlib
import json
import os
import subprocess
from datetime import datetime, timezone

from Crypto.Hash import keccak


BASE_ENDPOINT = "https://api.trongrid.io/wallet"
ENDPOINT = f"{BASE_ENDPOINT}/getcontractinfo"
OUTPUT = os.path.join(os.path.dirname(__file__), "testdata", "mainnet_contracts.json")

# USDC's proxy and implementation are deliberately both present.  The implementation gives the
# VM real application bytecode while the proxy exercises the short delegate/fallback runtime.
CONTRACTS = [
    {
        "name": "USDT (TetherToken)",
        "address": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
        "calls": [
            ("name", []), ("symbol", []), ("decimals", []), ("totalSupply", []),
            ("paused", []), ("owner", []), ("deprecated", []), ("maximumFee", []),
            ("basisPointsRate", []),
            ("balanceOf", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")]),
            ("allowance", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"),
                            ("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
            ("transfer", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"), ("uint256", "0")]),
            ("approve", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"), ("uint256", "0")]),
            ("transferFrom", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"),
                               ("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"), ("uint256", "0")]),
            ("getBlackListStatus", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")]),
            ("calcFee", [("uint256", "1000000")]),
        ],
    },
    {
        "name": "USDC implementation (FiatTokenV2_1)",
        "address": "TCLZsS5ZxqrHuPerpGKThYNmYErtPdP68L",
        "calls": [
            ("name", []), ("symbol", []), ("decimals", []), ("totalSupply", []),
            ("paused", []), ("owner", []), ("blacklister", []), ("masterMinter", []),
            ("pauser", []), ("rescuer", []), ("version", []),
            ("balanceOf", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
            ("allowance", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"),
                            ("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")]),
            ("transfer", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"), ("uint256", "0")]),
            ("approve", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"), ("uint256", "0")]),
            ("transferFrom", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"),
                               ("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"), ("uint256", "0")]),
            ("increaseAllowance", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"), ("uint256", "0")]),
            ("decreaseAllowance", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"), ("uint256", "0")]),
            ("isBlacklisted", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
            ("isMinter", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
            ("minterAllowance", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
            ("nonces", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
            ("authorizationState", [("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8"),
                                     ("bytes32", "0x" + "00" * 32)]),
        ],
    },
    {
        "name": "USDC proxy (FiatTokenProxy)",
        "address": "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8",
        "calls": [
            ("name", []), ("symbol", []), ("decimals", []), ("totalSupply", []),
            ("paused", []), ("owner", []),
            ("balanceOf", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")]),
            ("allowance", [("address", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"),
                            ("address", "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8")]),
        ],
    },
]


def call(endpoint, payload):
    output = subprocess.check_output([
        "curl", "-sS", "--max-time", "60", "-X", "POST", f"{BASE_ENDPOINT}/{endpoint}",
        "-H", "Content-Type: application/json", "-d", json.dumps(payload),
    ])
    return json.loads(output)


def base58_hex(value):
    alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
    number = 0
    for char in value:
        number = number * 58 + alphabet.index(char)
    raw = number.to_bytes((number.bit_length() + 7) // 8, "big")
    raw = b"\0" * (len(value) - len(value.lstrip("1"))) + raw
    checksum = raw[-4:]
    payload = raw[:-4]
    if hashlib.sha256(hashlib.sha256(payload).digest()).digest()[:4] != checksum:
        raise ValueError("invalid Base58Check address")
    return payload.hex()


def abi_word(type_name, value):
    if type_name == "address":
        address = base58_hex(value)
        if len(address) != 42 or not address.startswith("41"):
            raise ValueError(f"invalid TRON address: {value}")
        return "00" * 12 + address[2:]
    if type_name.startswith("uint") or type_name.startswith("int"):
        number = int(value, 0) if isinstance(value, str) else value
        if number < 0:
            number += 1 << 256
        return f"{number:064x}"
    if type_name == "bytes32":
        return value.removeprefix("0x").lower().zfill(64)
    raise ValueError(f"unsupported fixture ABI type: {type_name}")


def calldata(entrys, name, args):
    candidates = [entry for entry in entrys
                  if entry.get("type", "").lower() == "function" and entry.get("name") == name]
    for entry in candidates:
        inputs = entry.get("inputs", [])
        if len(inputs) == len(args):
            types = [item["type"] for item in inputs]
            signature = f"{name}({','.join(types)})"
            digest = keccak.new(digest_bits=256)
            digest.update(signature.encode())
            data = digest.hexdigest()[:8] + "".join(
                abi_word(type_name, value) for type_name, value in args)
            return {"method": signature, "args": [value for _, value in args], "data": data}
    raise ValueError(f"ABI has no matching function: {name}/{len(args)}")


def main():
    block = call("getnowblock", {})
    raw_header = block.get("block_header", {}).get("raw_data", {})
    fixture = {
        "schemaVersion": 1,
        "network": "mainnet",
        "capturedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "source": {
            "endpoint": ENDPOINT,
            "request": {"visible": True},
            "note": "runtimecode is deployed code, not init bytecode",
            "block": {
                "number": raw_header.get("number", 0),
                "id": block.get("blockID", ""),
                "timestamp": raw_header.get("timestamp", 0),
            },
        },
        "contracts": [],
    }
    for spec in CONTRACTS:
        response = call("getcontractinfo", {"value": spec["address"], "visible": True})
        smart_contract = response.get("smart_contract", {})
        runtime = response.get("runtimecode", "")
        entrys = smart_contract.get("abi", {}).get("entrys", [])
        if not runtime or not entrys:
            raise RuntimeError(f"missing runtime/ABI for {spec['address']}")
        fixture["contracts"].append({
            "name": spec["name"],
            "address": spec["address"],
            "addressHex": base58_hex(spec["address"]),
            "originAddress": smart_contract.get("origin_address", ""),
            "codeHash": smart_contract.get("code_hash", ""),
            "runtimeBytecode": runtime.lower(),
            "abi": smart_contract["abi"],
            "calldata": [calldata(entrys, name, args) for name, args in spec["calls"]],
        })
    os.makedirs(os.path.dirname(OUTPUT), exist_ok=True)
    with open(OUTPUT, "w") as output:
        json.dump(fixture, output, indent=2)
        output.write("\n")
    print(f"wrote {OUTPUT} ({len(fixture['contracts'])} contracts)")


if __name__ == "__main__":
    main()
