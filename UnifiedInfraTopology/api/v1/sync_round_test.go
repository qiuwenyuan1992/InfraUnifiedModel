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
	phases := make([]model.SyncRoundPhase, len(model.SyncRoundPhaseNames()))
	deviceIndex := -1
	for i, name := range model.SyncRoundPhaseNames() {
		phases[i] = model.SyncRoundPhase{Name: name, Position: i + 1, Status: "queued"}
		if name == "device" {
			deviceIndex = i
			phases[i] = model.SyncRoundPhase{Name: name, Position: i + 1, Status: "succeeded", Objects: 7, RunID: strings.Repeat("b", 32), StartedAt: &at}
		}
	}
	dto := InventoryRound(round, phases)
	for i, p := range dto.Phases {
		if p.Name != model.SyncRoundPhaseNames()[i] || p.Position != i+1 {
			t.Fatalf("unordered phases %+v", dto.Phases)
		}
	}
	if dto.Phases[deviceIndex].Objects != 7 || dto.Phases[deviceIndex].RunID == nil || dto.SnapshotAt.Location() != time.UTC || dto.Phases[0].RunID != nil {
		t.Fatalf("incorrect phase/time projection %+v", dto)
	}
	if dto.TotalPhases != len(phases) || dto.CompletedPhases != 1 || len(dto.Phases) != len(phases) {
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
