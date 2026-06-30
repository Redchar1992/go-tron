package bandwidth

import (
	"testing"

	"google.golang.org/protobuf/types/known/anypb"

	core "github.com/Redchar1992/go-tron/internal/proto/core"
)

func TestSizeMatchesSerialized(t *testing.T) {
	p, _ := anypb.New(&core.TransferContract{OwnerAddress: []byte{0x41, 1}, ToAddress: []byte{0x41, 2}, Amount: 5})
	tx := &core.Transaction{
		RawData:   &core.TransactionRaw{Contract: []*core.Transaction_Contract{{Type: core.Transaction_Contract_TransferContract, Parameter: p}}},
		Signature: [][]byte{make([]byte, 65)},
	}
	size, err := Size(tx)
	if err != nil {
		t.Fatal(err)
	}
	if size <= 0 {
		t.Fatalf("size = %d", size)
	}
	if got := BurnFee(size); got != int64(size)*10 {
		t.Fatalf("BurnFee = %d, want %d", got, int64(size)*10)
	}
}
