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

// Mainnet TVM fork activation versions.
//
// A TRON block header carries a `version` (BlockHeaderRaw.version). That version IS the
// authoritative fork key: java-tron gates every TVM feature by block version via
// ForkController.pass(ForkBlockVersionEnum.VERSION_X), and an SR only produces a block at
// version V once that fork has passed — so a block of version V has every feature whose
// activation version is <= V. We key on version rather than height/timestamp because
// java-tron's actual activation timing depends on the 80%-SR-upgrade stat (a runtime fact
// not derivable from height); the enum's hardForkTime is only a lower bound.
//
// Sources (local java-tron):
//   version numbers — common/.../config/Parameter.java ForkBlockVersionEnum
//   feature->version — actuator/.../utils/ProposalUtil.java (forkController.pass(VERSION_X))
const (
	verTransferTrc10  = 6  // VERSION_3_2_2   ProposalUtil.java:135 (ALLOW_TVM_TRANSFER_TRC10)
	verConstantinople = 8  // VERSION_3_6     ProposalUtil.java:203 (ALLOW_TVM_CONSTANTINOPLE)
	verSolidity059    = 9  // VERSION_3_6_5   ProposalUtil.java:218 (ALLOW_TVM_SOLIDITY_059)
	verIstanbul       = 19 // VERSION_4_1     ProposalUtil.java:337 (ALLOW_TVM_ISTANBUL)
	verLondon         = 23 // VERSION_4_4     ProposalUtil.java:528 (ALLOW_TVM_LONDON)
	verCompatibleEvm  = 23 // VERSION_4_4     ProposalUtil.java:539 (ALLOW_TVM_COMPATIBLE_EVM -> Forward6364)
	verHigherCPULimit = 24 // VERSION_4_5     ProposalUtil.java:550 (ALLOW_HIGHER_LIMIT... -> !LegacyMemCost)

	// LatestForkVersion is the newest ForkBlockVersionEnum value we model
	// (VERSION_4_8_1_1, Parameter.java). At/after it VMConfigForVersion == LatestVMConfig.
	LatestForkVersion = 35
)

// VMConfigForVersion returns the TVM hardfork gates active for a mainnet block of the
// given header version, matching java-tron's per-version proposal gating (see the version
// constants above). Version 0 (pre-everything) yields the original TRON opcode set only.
func VMConfigForVersion(version int32) VMConfig {
	on := func(v int) bool { return int(version) >= v }
	return VMConfig{
		AllowTransferTrc10:  on(verTransferTrc10),
		AllowConstantinople: on(verConstantinople),
		AllowSolidity059:    on(verSolidity059),
		AllowIstanbul:       on(verIstanbul),
		AllowLondon:         on(verLondon),
		Forward6364:         on(verCompatibleEvm),
		// LegacyMemCost is the PRE-HigherLimit memory cost; the surcharge turns on with it.
		LegacyMemCost: !on(verHigherCPULimit),
	}
}
