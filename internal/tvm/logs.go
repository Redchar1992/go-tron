package tvm

// LOG0..LOG4 event emission. Logs are collected on the EVM across all call frames and
// journaled to the same snapshots as state: a frame that REVERTs or faults discards the
// logs it (and its children) emitted, exactly like its storage writes. CONSENSUS-CRITICAL;
// energy + semantics mirror java-tron EnergyCost.getLogCost / OperationActions.logAction.

// Log is an event emitted by a LOGn opcode: the emitting contract's address, up to four
// indexed topics, and the memory data payload (java-tron LogInfo).
type Log struct {
	Address []byte     // emitting contract address (the frame's Self)
	Topics  [][32]byte // 0..4 indexed topics
	Data    []byte     // memory payload [offset, offset+size)
}

// Logs returns the transaction's surviving logs (those not discarded by a revert), in
// emission order. The actuator harvests these only when the top-level frame did not revert.
func (evm *EVM) Logs() []*Log { return evm.logs }

// log energy constants (java-tron EnergyCost): base + per-topic + per-data-byte.
const (
	logEnergy      = 375
	logTopicEnergy = 375
	logDataEnergy  = 8
)

// gasLog builds the LOGn energy function: 375 + 375*n + 8*size + memory expansion over
// [offset, offset+size). Stack (top-down): offset, size, topic0..topic(n-1).
func gasLog(n int) func(*interpreter, *scope) (uint64, error) {
	return func(_ *interpreter, sc *scope) (uint64, error) {
		offset := sc.stack.peek(0)
		size := sc.stack.peek(1)
		end, err := memAccessSize(offset, size)
		if err != nil {
			return 0, err
		}
		exp, err := memExpandCost(sc.mem, end)
		if err != nil {
			return 0, err
		}
		sz, err := toUint64(size)
		if err != nil {
			return 0, err
		}
		return logEnergy + logTopicEnergy*uint64(n) + logDataEnergy*sz + exp, nil
	}
}

// opLog builds the LOGn executor: pop offset, size, and n topics; snapshot the memory
// payload; append a Log for the executing contract. LOG is state-modifying, so it faults in
// a STATICCALL/read-only context (java-tron requireNoStaticCall).
func opLog(n int) func(*interpreter, *scope) error {
	return func(in *interpreter, sc *scope) error {
		if in.readOnly {
			return ErrStaticStateChange
		}
		offset, _ := sc.stack.pop()
		size, _ := sc.stack.pop()
		topics := make([][32]byte, n)
		for i := 0; i < n; i++ {
			t, _ := sc.stack.pop()
			topics[i] = t.Bytes32()
		}
		data := sc.mem.get(offset.Uint64(), size.Uint64())
		in.evm.logs = append(in.evm.logs, &Log{
			Address: append([]byte(nil), sc.contract.Self...),
			Topics:  topics,
			Data:    append([]byte(nil), data...),
		})
		return nil
	}
}
