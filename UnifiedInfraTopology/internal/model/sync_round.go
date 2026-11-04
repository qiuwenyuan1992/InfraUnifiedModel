package model

import "time"

type SyncRound struct {
	ID                string `gorm:"primaryKey"`
	SourceID          string
	Status            string
	Mode              string
	RequestHash       string
	IdempotencyKey    string
	RequestedBy       string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	SnapshotAt        *time.Time
	CancelRequestedAt *time.Time
	LeaseExpiresAt    *time.Time
	ErrorCode         string
}

func (SyncRound) TableName() string { return "sync_rounds" }

type SyncRoundPhase struct {
	RoundID          string `gorm:"primaryKey"`
	Name             string `gorm:"primaryKey"`
	Position         int
	Status           string
	RunID            string
	StartedAt        *time.Time
	FinishedAt       *time.Time
	Nodes            int64
	Edges            int64
	Objects          int64
	Skipped          int64
	MissingEndpoints int64
	ErrorCode        string
}

func (SyncRoundPhase) TableName() string { return "sync_round_phases" }

func SyncRoundPhaseNames() []string {
	return []string{"data_center", "cabinet", "device", "transformer", "ups_group", "ups", "rpp", "pod", "interface", "power_relations", "pod_membership", "spatial_relations", "gpu_sync", "server_uplinks", "links_to"}
}
