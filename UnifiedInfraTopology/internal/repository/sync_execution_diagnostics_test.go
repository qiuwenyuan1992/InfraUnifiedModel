package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"errors"
	"testing"
	"time"
)

func TestInventoryExecutionDiagnosticPersistsThroughRunRepository(t *testing.T) {
	_, base, db := inventoryRepositoryFixture(t)
	if err := db.AutoMigrate(&model.SyncDiagnostic{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SyncRun{ID: "run", SourceID: "source", Status: model.SyncRunStatusRunning, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	control := NewInventoryRunRepository(base, "run")
	recorder, ok := control.(interface {
		RecordSyncExecutionDiagnostic(context.Context, *model.SyncDiagnostic) error
	})
	if !ok {
		t.Fatal("concrete run repository does not expose diagnostic persistence")
	}
	diagnostic := &model.SyncDiagnostic{RunID: "run", SourceID: "source", Resource: "gpu_sync", Severity: "error", Code: "gpu_source_changed", Detail: `{"stage":"source_changed","original_objects":5,"reread_objects":6}`, CreatedAt: time.Now()}
	if err := recorder.RecordSyncExecutionDiagnostic(context.Background(), diagnostic); err != nil {
		t.Fatal(err)
	}
	var stored model.SyncDiagnostic
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" || stored.Code != diagnostic.Code || stored.Detail != diagnostic.Detail || stored.Resource != "gpu_sync" {
		t.Fatalf("diagnostic lost: %+v", stored)
	}
	diagnostic.SourceID = "wrong"
	if err := recorder.RecordSyncExecutionDiagnostic(context.Background(), diagnostic); !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("source ownership not checked: %v", err)
	}
}
