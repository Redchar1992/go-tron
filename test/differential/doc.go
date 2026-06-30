// Package differential is the replay harness that makes or breaks the project: replay
// mainnet blocks through go-tron and compare state root + block hash + per-tx receipts/
// energy against a java-tron oracle at every height. Any mismatch is a P0 with an exact
// reproducing block. Wired from M2 onward. M0: placeholder.
package differential
