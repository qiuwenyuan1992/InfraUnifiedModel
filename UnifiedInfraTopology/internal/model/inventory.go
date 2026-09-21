package model

import "time"

// 持久化结构不承担 HTTP 契约；字段类型由显式版本迁移定义。
type InventoryState struct {
	ID                 uint `gorm:"primaryKey"`
	ActiveGenerationID *string
	ProjectionState    string `gorm:"default:uninitialized"`
	ProjectionEpoch    int64
}

func (InventoryState) TableName() string { return "inventory_state" }

type Source struct {
	ID          string `gorm:"primaryKey"`
	Name        string
	AdapterKind string
	ConfigRef   string
	Enabled     bool
}

func (Source) TableName() string { return "sources" }

type Generation struct {
	ID             string `gorm:"primaryKey"`
	RunID          string
	State          string
	InventoryReady bool
	GraphReady     bool
	RoutingReady   bool
	CreatedAt      time.Time
	PublishedAt    *time.Time
}

func (Generation) TableName() string { return "generations" }

// 资产结构表示图中的当前状态，GenerationID 仅由读取服务注入响应批次。
type Device struct {
	GenerationID     string
	EntityID         string
	Name             string
	DeviceKind       string
	Role             string
	Lifecycle        string
	ResolutionStatus string
	SerialNumber     *string
}

type Interface struct {
	GenerationID     string
	EntityID         string
	DeviceID         string
	Namespace        string
	SourceName       string
	NormalizedName   string
	InterfaceKind    string
	AdminState       string
	OperState        string
	Lifecycle        string
	ResolutionStatus string
	SpeedBPS         *int64
}

type Address struct {
	GenerationID     string
	EntityID         string
	DeviceID         string
	InterfaceID      *string
	AddressFamily    int
	Address          []byte
	PrefixLength     *int
	AddressScopeKey  string
	ScopeStatus      string
	Purpose          string
	Lifecycle        string
	ResolutionStatus string
}

type SyncRun struct {
	ID                string `gorm:"primaryKey"`
	Status            string
	Mode              string
	BaseGenerationID  *string
	GenerationID      *string
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

type SyncRunSource struct {
	RunID    string `gorm:"primaryKey"`
	SourceID string `gorm:"primaryKey"`
	Status   string
}

func (SyncRunSource) TableName() string { return "sync_run_sources" }

type Entity struct {
	ID        string `gorm:"primaryKey"`
	Kind      string
	CreatedAt time.Time
}

func (Entity) TableName() string { return "entities" }

type SourceKey struct {
	ID         string `gorm:"primaryKey"`
	SourceID   string
	KeyHash    []byte
	ObjectType string
	Namespace  string
	NativeID   string
}

func (SourceKey) TableName() string { return "source_keys" }

type IdentityBinding struct {
	ID                  string `gorm:"primaryKey"`
	SourceKeyID         string
	EntityID            string
	Incarnation         int64
	FirstGenerationID   string
	RetiredGenerationID *string
}

func (IdentityBinding) TableName() string { return "identity_bindings" }
