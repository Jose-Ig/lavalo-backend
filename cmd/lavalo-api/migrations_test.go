package main

import (
	"path/filepath"
	"testing"

	"github.com/Jose-Ig/lavalo-backend/internal/common"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRecoverySchemaMigrationIsRepeatable(t *testing.T) {
	common.Logger = zap.NewNop()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "schema.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	defer raw.Close()
	for i := 0; i < 2; i++ {
		if err = runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&operations.Session{}, &operations.Execution{}, &operations.Incident{}, &operations.Boot{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing table for %T", model)
		}
	}
	if !db.Migrator().HasColumn("reservations", "parent_execution_id") || !db.Migrator().HasColumn("devices", "protocol") {
		t.Fatal("missing recovery columns")
	}
}
