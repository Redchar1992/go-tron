package tvm

// Fork presets. On a real network the active gates are derived from committee-proposal
// state by the node; these named configs are for tests and for running the VM in a known
// era. The zero VMConfig is "pre-everything" (only the original TRON opcode set).

// LatestVMConfig enables every implemented hardfork gate — current mainnet behavior
// (TransferTrc10 + Constantinople + Solidity059 + Istanbul + London), plus the 63/64
// energy forwarding and the modern memory-cost surcharge.
func LatestVMConfig() VMConfig {
	return VMConfig{
		Forward6364:         true,
		AllowTransferTrc10:  true,
		AllowConstantinople: true,
		AllowSolidity059:    true,
		AllowIstanbul:       true,
		AllowLondon:         true,
	}
}

// ConstantinopleVMConfig enables up to and including Constantinople (SHL/SHR/SAR,
// CREATE2, EXTCODEHASH) but not Solidity059/Istanbul/London.
func ConstantinopleVMConfig() VMConfig {
	return VMConfig{AllowTransferTrc10: true, AllowConstantinople: true}
}
