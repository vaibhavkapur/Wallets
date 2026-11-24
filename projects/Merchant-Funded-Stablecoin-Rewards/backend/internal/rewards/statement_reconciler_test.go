package rewards

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStatementReconcilerMatchesSharedFixture(t *testing.T) {
	inPath := filepath.Join("..", "..", "..", "fixtures", "statement_batch_input.json")
	expPath := filepath.Join("..", "..", "..", "fixtures", "statement_batch_expected.json")

	inBytes, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatalf("read input fixture: %v", err)
	}
	var batch StatementBatchInput
	if err := json.Unmarshal(inBytes, &batch); err != nil {
		t.Fatalf("unmarshal input fixture: %v", err)
	}

	expBytes, err := os.ReadFile(expPath)
	if err != nil {
		t.Fatalf("read expected fixture: %v", err)
	}
	var expected StatementReconciliationReport
	if err := json.Unmarshal(expBytes, &expected); err != nil {
		t.Fatalf("unmarshal expected fixture: %v", err)
	}

	got := ReconcileStatementBatch(batch)
	if !reflect.DeepEqual(got, expected) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		expJSON, _ := json.MarshalIndent(expected, "", "  ")
		t.Fatalf("reconciliation mismatch:\ngot:\n%s\nwant:\n%s", gotJSON, expJSON)
	}
}
