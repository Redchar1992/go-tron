// Package consensus implements DPoS: 27 active witnesses, slot/timestamp validation,
// the scheduled single-producer loop, maintenance-period schedule updates, and
// solid-block calculation (mirrors java-tron's DposService/DposTask).
//
// CONSENSUS-CRITICAL. Target: M2 (validation) / M7 (production). M0: placeholder.
package consensus
