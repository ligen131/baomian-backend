package migration

import (
	"io/fs"
	"strings"
	"testing"
)

func TestContinuousVoiceRunMigrationCarriesRunIdentityAndRemovesThreeTurnLimits(t *testing.T) {
	up, err := fs.ReadFile(Files, "sql/000004_continuous_voice_runs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(up)
	for _, expected := range []string{
		"CREATE TABLE conversation_runs",
		"status IN ('active', 'finishing', 'completed', 'aborted')",
		"idx_conversation_runs_active_device",
		"ALTER TABLE conversation_turns ADD COLUMN run_id UUID",
		"ON conversation_turns (run_id, client_request_id, role)",
		"CREATE UNIQUE INDEX idx_memory_cards_run ON memory_cards (run_id)",
		"CHECK (turn_index >= 1)",
		"CHECK (conversation_turns >= 0)",
	} {
		if !strings.Contains(contract, expected) {
			t.Errorf("migration missing %q", expected)
		}
	}
	for _, forbidden := range []string{
		"CHECK (turn_index BETWEEN 1 AND 3)",
		"CHECK (conversation_turns BETWEEN 0 AND 3)",
	} {
		if strings.Contains(contract, forbidden) {
			t.Errorf("migration retains %q", forbidden)
		}
	}
}
