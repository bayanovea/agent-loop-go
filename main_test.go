package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestLogTimeReplayIsIdempotent covers the transport-replay case from RESULTS.md:
// the same tool_use.id arriving twice must apply the delta once and return a
// result that is byte-identical to the first one.
func TestLogTimeReplayIsIdempotent(t *testing.T) {
	tasks = []Task{{ID: "1", Title: "database migration"}}
	ledger := map[string]logTimeResult{}

	first, err := logTime(ledger, "toolu_01", "1", 1800)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := logTime(ledger, "toolu_01", "1", 1800)
	if err != nil {
		t.Fatalf("replayed call: %v", err)
	}

	if got := tasks[0].Time; got != 1800 {
		t.Fatalf("logged time applied twice: got %d, want 1800", got)
	}

	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatalf("replay result differs from original:\n first: %s\nsecond: %s", a, b)
	}

	// A new tool_use.id is a new call and must apply again. This is the
	// "model re-asks" case the ledger cannot catch; the test pins that boundary.
	if _, err := logTime(ledger, "toolu_02", "1", 1800); err != nil {
		t.Fatalf("new id: %v", err)
	}
	if got := tasks[0].Time; got != 3600 {
		t.Fatalf("new tool_use.id not applied: got %d, want 3600", got)
	}
}

func TestLogTimeRejectsNonPositiveSeconds(t *testing.T) {
	tasks = []Task{{ID: "1", Title: "database migration"}}
	ledger := map[string]logTimeResult{}
	for _, s := range []int{0, -3600} {
		if _, err := logTime(ledger, "toolu_bad", "1", s); err == nil {
			t.Errorf("seconds=%d accepted, want error", s)
		}
	}
	if tasks[0].Time != 0 || len(ledger) != 0 {
		t.Errorf("rejected call mutated state: time=%d ledger=%d", tasks[0].Time, len(ledger))
	}
}
