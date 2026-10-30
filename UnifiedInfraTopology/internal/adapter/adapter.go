// Package adapter 定义外部数据源边界，不负责本项目的持久化或发布。
package adapter

import (
	"context"

	"UnifiedInfraTopology/internal/model"
)

// SourceAdapter 定义来源接入前检查。
// 实现不得在错误或日志中暴露 Source.ConfigRef 对应的凭据。
type SourceAdapter interface {
	Validate(context.Context, model.Source) error
}

// DeviceCollector 流式采集设备，调用方负责持久化和发布。
type DeviceCollector interface {
	CollectDevices(context.Context, model.Source, func(DeviceRecord) error) error
}

// DevicePageCollector 按偏移量采集一页设备，调用方负责推进分页。
type DevicePageCollector interface {
	CollectDevicePage(context.Context, model.Source, int) (DevicePage, error)
}

// EntityPageCollector 按实体类型分页采集，完整性由调用方验证。
type EntityPageCollector interface {
	SelectedEntity(model.Source) (model.EntityType, error)
	CollectEntityPage(context.Context, model.Source, model.EntityType, int) (EntityPage, error)
	CoversFullSource(model.Source, model.EntityType) (bool, error)
}

// EntityPageStream 表示单次完整采集使用的独立分页视图。
type EntityPageStream interface {
	Page(context.Context, int) (EntityPage, error)
}

// EntityPassCollector 为每次验证采集建立独立分页视图。
type EntityPassCollector interface {
	OpenEntityPass(context.Context, model.Source, model.EntityType) (EntityPageStream, error)
}

// EntityPage 是一页实体采集结果。
type EntityPage struct {
	Records    []EntityRecord
	NextOffset int
	Total      int
	Done       bool
}

// EntityRecord 将来源数字引用与稳定身份分开，不包含同步生成时间。
type EntityRecord struct {
	Partition        int64
	SourceInstanceID int64
	Identity         string
	OwnerDeviceSN    string
	Properties       map[string]any
}

// DevicePage 是一页设备采集结果。
type DevicePage struct {
	Records    []DeviceRecord
	NextOffset int
	Total      int
	Done       bool
}

// DeviceRecord 是 device_view 转换后的领域采集记录。
type DeviceRecord struct {
	SourceInstanceID int64
	DeviceSN         string
	Name             string
	ParentTypeID     int64
	DeviceTypeID     int64
	Role             string
	CabinetUUID      string
	PodUUID          string
	ComputePlane     []ComputePlaneReference
	AllIPs           []string
}

type ComputePlaneReference struct {
	PodID int64
}
