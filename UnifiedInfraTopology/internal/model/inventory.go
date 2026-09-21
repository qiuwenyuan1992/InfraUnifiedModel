package model

import "time"

// 持久化结构不承担 HTTP 契约；字段类型由显式版本迁移定义。
type Source struct {
	ID          string `gorm:"primaryKey"`
	Name        string
	AdapterKind string
	ConfigRef   string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (Source) TableName() string { return "sources" }

type SyncRun struct {
	ID                string `gorm:"primaryKey"`
	SourceID          string
	Status            string
	Mode              string
	RequestHash       string
	IdempotencyKey    string
	RequestedBy       string
	CancelRequestedAt *time.Time
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ErrorCode         string
}

func (SyncRun) TableName() string { return "sync_runs" }

type SyncCheckpoint struct {
	SourceID    string `gorm:"primaryKey"`
	Resource    string `gorm:"primaryKey"`
	Cursor      string
	Complete    bool
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

func (SyncCheckpoint) TableName() string { return "sync_checkpoints" }

type SyncDiagnostic struct {
	ID        string `gorm:"primaryKey"`
	RunID     string
	SourceID  string
	Resource  string
	Severity  string
	Code      string
	ObjectRef string
	FieldPath string
	Detail    string
	CreatedAt time.Time
}

func (SyncDiagnostic) TableName() string { return "sync_diagnostics" }

type Publication struct {
	ID            string `gorm:"primaryKey"`
	RunID         string
	SourceID      string
	Version       int64
	SchemaVersion int
	Status        string
	PublishedAt   *time.Time
	CreatedAt     time.Time
}

func (Publication) TableName() string { return "publications" }
