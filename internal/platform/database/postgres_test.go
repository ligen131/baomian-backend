package database

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestDatabaseLoggerSuppressesRecordNotFoundWarnings(t *testing.T) {
	var output bytes.Buffer
	configured := databaseLogger(log.New(&output, "", 0))

	configured.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT empty queue", 0
	}, gorm.ErrRecordNotFound)
	if output.Len() != 0 {
		t.Fatalf("record not found was logged: %q", output.String())
	}

	configured.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT broken", 0
	}, errors.New("database unavailable"))
	if !strings.Contains(output.String(), "database unavailable") {
		t.Fatalf("real database error was not logged: %q", output.String())
	}
}
