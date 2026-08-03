package vmoracle

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRequestJSONRoundTrip(t *testing.T) {
	world, tx := baseWorld("00", 0, 0, 0)
	payload, err := json.Marshal(Request{World: &world, Tx: &tx})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded Request
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if decoded.World == nil || decoded.Tx == nil || decoded.Tx.Contract != tx.Contract {
		t.Fatalf("decoded request lost world/tx: %+v", decoded)
	}
}

func TestExecutionJSONUsesReturnField(t *testing.T) {
	want := Execution{Result: "SUCCESS", Return: "2a", EnergyUsed: 7}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal execution: %v", err)
	}
	if string(payload) == "" {
		t.Fatal("empty execution JSON")
	}
	var got Execution
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal execution: %v", err)
	}
	if got.Return != want.Return || got.EnergyUsed != want.EnergyUsed {
		t.Fatalf("execution round trip = %+v, want %+v", got, want)
	}
}

func TestJavaOracleIntegration(t *testing.T) {
	if os.Getenv("JTRON_ORACLE_INTEGRATION") != "1" {
		t.Skip("set JTRON_ORACLE_INTEGRATION=1 to run the JDK 17 java-tron oracle")
	}
	client, err := NewOracleClientFromEnv()
	if err != nil {
		t.Fatalf("start java oracle: %v", err)
	}
	defer func() { _ = client.Close() }()
	pong, err := client.Ping()
	if err != nil {
		t.Fatalf("java oracle ping: %v", err)
	}
	if pong["status"] != "ok" || pong["javaTronVersion"] != "GreatVoyage-v4.8.1.1" {
		t.Fatalf("unexpected ping response: %+v", pong)
	}
	world, tx := baseWorld(storeLogReturn(), 0, 0, 0)
	got, err := client.Execute(world, tx)
	if err != nil {
		t.Fatalf("java oracle execute: %v", err)
	}
	if got.Result != "SUCCESS" || got.Return == "" || got.EnergyUsed == 0 {
		t.Fatalf("unexpected java execution: %+v", got)
	}
	local, err := Execute(world, tx)
	if err != nil {
		t.Fatalf("go execution: %v", err)
	}
	if differences := Diff(local, got); len(differences) != 0 {
		t.Fatalf("go/java execution diverged: %+v\ngo=%+v\njava=%+v", differences, local, got)
	}

	stakedWorld, stakedTx := baseWorld(storeLogReturn(), 1_000_000, 1, 1_000_000_000)
	stakedJava, err := client.Execute(stakedWorld, stakedTx)
	if err != nil {
		t.Fatalf("java oracle staked execute: %v", err)
	}
	stakedGo, err := Execute(stakedWorld, stakedTx)
	if err != nil {
		t.Fatalf("go staked execute: %v", err)
	}
	if differences := Diff(stakedGo, stakedJava); len(differences) != 0 {
		t.Fatalf("staked go/java execution diverged: %+v\ngo=%+v\njava=%+v",
			differences, stakedGo, stakedJava)
	}
}
