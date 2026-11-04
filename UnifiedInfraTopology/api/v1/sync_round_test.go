package v1

import (
	"UnifiedInfraTopology/internal/model"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInventoryRoundFixedProgressAndSafeProjection(t *testing.T) {
	at := time.Date(2026, 11, 3, 9, 0, 0, 123000, time.FixedZone("local", 3600))
	round := model.SyncRound{ID: strings.Repeat("a", 32), RequestHash: "secret-hash", IdempotencyKey: "secret-key", SnapshotAt: &at}
	dto := InventoryRound(round, []model.SyncRoundPhase{{Name: "device", Status: "succeeded", Position: 8, Objects: 7, RunID: strings.Repeat("b", 32), StartedAt: &at}})
	for i, p := range dto.Phases {
		if p.Name != model.SyncRoundPhaseNames()[i] || p.Position != i+1 {
			t.Fatalf("unordered phases %+v", dto.Phases)
		}
	}
	if dto.Phases[2].Objects != 7 || dto.Phases[2].RunID == nil || dto.SnapshotAt.Location() != time.UTC || dto.Phases[0].RunID != nil {
		t.Fatalf("incorrect phase/time projection %+v", dto)
	}
	if dto.TotalPhases != 15 || dto.CompletedPhases != 1 || len(dto.Phases) != 15 {
		t.Fatalf("invalid progress: %+v", dto)
	}
	data, _ := json.Marshal(dto)
	for _, secret := range []string{"secret-hash", "secret-key", "percent"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("unsafe DTO %s", data)
		}
	}
}

func TestInventoryDiagnosticOmitsDetail(t *testing.T) {
	data, _ := json.Marshal(InventoryDiagnostic(model.SyncDiagnostic{Code: "missing_endpoint", Detail: "mysql://secret"}))
	if strings.Contains(string(data), "detail") || strings.Contains(string(data), "secret") {
		t.Fatalf("unsafe diagnostic %s", data)
	}
}
