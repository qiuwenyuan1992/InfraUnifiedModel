package v1

import (
	"sort"
	"time"

	"UnifiedInfraTopology/internal/model"
)

type EnqueueInventoryRoundRequest struct {
	SourceID string `json:"source_id"`
	Mode     string `json:"mode"`
}

type InventoryRoundPageDTO struct {
	Items      []InventoryRoundDTO `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

type InventoryDiagnosticPageDTO struct {
	Items      []InventoryDiagnosticDTO `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

type InventoryRoundDTO struct {
	ID                string                   `json:"id"`
	SourceID          string                   `json:"source_id"`
	Status            string                   `json:"status"`
	Mode              string                   `json:"mode"`
	CreatedAt         time.Time                `json:"created_at"`
	StartedAt         *time.Time               `json:"started_at"`
	FinishedAt        *time.Time               `json:"finished_at"`
	SnapshotAt        *time.Time               `json:"snapshot_at"`
	CancelRequestedAt *time.Time               `json:"cancel_requested_at"`
	ErrorCode         string                   `json:"error_code"`
	CompletedPhases   int                      `json:"completed_phases"`
	TotalPhases       int                      `json:"total_phases"`
	Phases            []InventoryRoundPhaseDTO `json:"phases,omitempty"`
}

type InventoryRoundPhaseDTO struct {
	Name             string     `json:"name"`
	Position         int        `json:"position"`
	Status           string     `json:"status"`
	RunID            *string    `json:"run_id"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	Nodes            int64      `json:"nodes"`
	Edges            int64      `json:"edges"`
	Objects          int64      `json:"objects"`
	Skipped          int64      `json:"skipped"`
	MissingEndpoints int64      `json:"missing_endpoints"`
	ErrorCode        string     `json:"error_code"`
}

type InventoryDiagnosticDTO struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	SourceID  string    `json:"source_id"`
	Resource  string    `json:"resource"`
	Severity  string    `json:"severity"`
	Code      string    `json:"code"`
	ObjectRef string    `json:"object_ref"`
	FieldPath string    `json:"field_path"`
	CreatedAt time.Time `json:"created_at"`
}

func InventoryRound(round model.SyncRound, phases []model.SyncRoundPhase) InventoryRoundDTO {
	dto := InventoryRoundDTO{ID: round.ID, SourceID: round.SourceID, Status: round.Status, Mode: round.Mode,
		CreatedAt: round.CreatedAt.UTC(), StartedAt: inventoryUTCTime(round.StartedAt), FinishedAt: inventoryUTCTime(round.FinishedAt),
		SnapshotAt: inventoryUTCTime(round.SnapshotAt), CancelRequestedAt: inventoryUTCTime(round.CancelRequestedAt), ErrorCode: round.ErrorCode}
	names := model.SyncRoundPhaseNames()
	if len(phases) > 0 {
		// 展示任务创建时记录的阶段，新增room不改变旧任务的15阶段历史。
		ordered := append([]model.SyncRoundPhase(nil), phases...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
		names = make([]string, len(ordered))
		for i, phase := range ordered {
			names[i] = phase.Name
		}
	}
	dto.TotalPhases = len(names)
	dto.Phases = make([]InventoryRoundPhaseDTO, len(names))
	for i, name := range names {
		phase := InventoryRoundPhaseDTO{Name: name, Position: i + 1, Status: "not_run"}
		for _, stored := range phases {
			if stored.Name != name {
				continue
			}
			phase.Status = stored.Status
			phase.StartedAt = inventoryUTCTime(stored.StartedAt)
			phase.FinishedAt = inventoryUTCTime(stored.FinishedAt)
			if stored.RunID != "" {
				id := stored.RunID
				phase.RunID = &id
			}
			phase.Nodes = stored.Nodes
			phase.Edges = stored.Edges
			phase.Objects = stored.Objects
			phase.Skipped = stored.Skipped
			phase.MissingEndpoints = stored.MissingEndpoints
			phase.ErrorCode = stored.ErrorCode
			break
		}
		if phase.Status == "succeeded" {
			dto.CompletedPhases++
		}
		dto.Phases[i] = phase
	}
	return dto
}

func InventoryDiagnostic(item model.SyncDiagnostic) InventoryDiagnosticDTO {
	return InventoryDiagnosticDTO{ID: item.ID, RunID: item.RunID, SourceID: item.SourceID, Resource: item.Resource, Severity: item.Severity, Code: item.Code, ObjectRef: item.ObjectRef, FieldPath: item.FieldPath, CreatedAt: item.CreatedAt.UTC()}
}
