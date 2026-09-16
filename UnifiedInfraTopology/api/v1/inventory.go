package v1

import (
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"UnifiedInfraTopology/internal/model"
)

type InventoryScopeDTO struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	ActiveGenerationID *string `json:"active_generation_id"`
}

type InventorySourceDTO struct {
	ID          string `json:"id"`
	ScopeID     string `json:"scope_id"`
	Name        string `json:"name"`
	AdapterKind string `json:"adapter_kind"`
	Enabled     bool   `json:"enabled"`
}

type InventoryGenerationDTO struct {
	ID             string     `json:"id"`
	ScopeID        string     `json:"scope_id"`
	RunID          string     `json:"run_id"`
	State          string     `json:"state"`
	InventoryReady bool       `json:"inventory_ready"`
	GraphReady     bool       `json:"graph_ready"`
	RoutingReady   bool       `json:"routing_ready"`
	CreatedAt      time.Time  `json:"created_at"`
	PublishedAt    *time.Time `json:"published_at"`
}

type InventoryDeviceDTO struct {
	EntityID         string  `json:"entity_id"`
	GenerationID     string  `json:"generation_id"`
	Name             string  `json:"name"`
	DeviceKind       string  `json:"device_kind"`
	Role             string  `json:"role"`
	Lifecycle        string  `json:"lifecycle"`
	ResolutionStatus string  `json:"resolution_status"`
	SerialNumber     *string `json:"serial_number"`
}

type InventoryInterfaceDTO struct {
	EntityID         string  `json:"entity_id"`
	GenerationID     string  `json:"generation_id"`
	DeviceID         string  `json:"device_id"`
	Namespace        string  `json:"namespace"`
	SourceName       string  `json:"source_name"`
	NormalizedName   string  `json:"normalized_name"`
	InterfaceKind    string  `json:"interface_kind"`
	AdminState       string  `json:"admin_state"`
	OperState        string  `json:"oper_state"`
	Lifecycle        string  `json:"lifecycle"`
	ResolutionStatus string  `json:"resolution_status"`
	SpeedBPS         *string `json:"speed_bps"`
}

type InventoryAddressDTO struct {
	EntityID         string  `json:"entity_id"`
	GenerationID     string  `json:"generation_id"`
	DeviceID         string  `json:"device_id"`
	InterfaceID      *string `json:"interface_id"`
	AddressFamily    int     `json:"address_family"`
	Address          string  `json:"address"`
	PrefixLength     *int    `json:"prefix_length"`
	AddressScopeKey  string  `json:"address_scope_key"`
	ScopeStatus      string  `json:"scope_status"`
	Purpose          string  `json:"purpose"`
	Lifecycle        string  `json:"lifecycle"`
	ResolutionStatus string  `json:"resolution_status"`
}

type InventoryRunDTO struct {
	ID                string     `json:"id"`
	ScopeID           string     `json:"scope_id"`
	Status            string     `json:"status"`
	Mode              string     `json:"mode"`
	BaseGenerationID  *string    `json:"base_generation_id"`
	GenerationID      *string    `json:"generation_id"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	ErrorCode         string     `json:"error_code"`
}

func InventoryItems(items interface{}) (interface{}, error) {
	switch items := items.(type) {
	case []model.TopologyScope:
		result := make([]InventoryScopeDTO, len(items))
		for i, item := range items {
			result[i] = InventoryScopeDTO{
				ID: item.ID, Name: item.Name, ActiveGenerationID: item.ActiveGenerationID,
			}
		}
		return result, nil
	case []model.Source:
		result := make([]InventorySourceDTO, len(items))
		for i, item := range items {
			result[i] = InventorySourceDTO{
				ID: item.ID, ScopeID: item.ScopeID, Name: item.Name,
				AdapterKind: item.AdapterKind, Enabled: item.Enabled,
			}
		}
		return result, nil
	case []model.Generation:
		result := make([]InventoryGenerationDTO, len(items))
		for i, item := range items {
			result[i] = InventoryGeneration(item)
		}
		return result, nil
	case []model.Device:
		result := make([]InventoryDeviceDTO, len(items))
		for i, item := range items {
			result[i] = InventoryDevice(item)
		}
		return result, nil
	case []model.Interface:
		result := make([]InventoryInterfaceDTO, len(items))
		for i, item := range items {
			var speed *string
			if item.SpeedBPS != nil {
				value := strconv.FormatInt(*item.SpeedBPS, 10)
				speed = &value
			}
			result[i] = InventoryInterfaceDTO{
				EntityID: item.EntityID, GenerationID: item.GenerationID, DeviceID: item.DeviceID,
				Namespace: item.Namespace, SourceName: item.SourceName, NormalizedName: item.NormalizedName,
				InterfaceKind: item.InterfaceKind, AdminState: item.AdminState, OperState: item.OperState,
				Lifecycle: item.Lifecycle, ResolutionStatus: item.ResolutionStatus, SpeedBPS: speed,
			}
		}
		return result, nil
	case []model.Address:
		result := make([]InventoryAddressDTO, len(items))
		for i, item := range items {
			address, ok := netip.AddrFromSlice(item.Address)
			// 领域模型的 IPv4 使用 16 字节 mapped 编码，由 family 决定文本表示。
			if item.AddressFamily == 4 {
				address = address.Unmap()
			}
			if !ok || (item.AddressFamily != 4 && item.AddressFamily != 6) ||
				(item.AddressFamily == 4 && !address.Is4()) || (item.AddressFamily == 6 && !address.Is6()) {
				return nil, fmt.Errorf("invalid IP address bytes for inventory address %q", item.EntityID)
			}
			result[i] = InventoryAddressDTO{
				EntityID: item.EntityID, GenerationID: item.GenerationID, DeviceID: item.DeviceID,
				InterfaceID: item.InterfaceID, AddressFamily: item.AddressFamily, Address: address.String(),
				PrefixLength: item.PrefixLength, AddressScopeKey: item.AddressScopeKey, ScopeStatus: item.ScopeStatus,
				Purpose: item.Purpose, Lifecycle: item.Lifecycle, ResolutionStatus: item.ResolutionStatus,
			}
		}
		return result, nil
	case []model.SyncRun:
		result := make([]InventoryRunDTO, len(items))
		for i, item := range items {
			result[i] = InventoryRun(item)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported inventory items type %T", items)
	}
}

func InventoryDevice(item model.Device) InventoryDeviceDTO {
	return InventoryDeviceDTO{
		EntityID: item.EntityID, GenerationID: item.GenerationID, Name: item.Name,
		DeviceKind: item.DeviceKind, Role: item.Role, Lifecycle: item.Lifecycle,
		ResolutionStatus: item.ResolutionStatus, SerialNumber: item.SerialNumber,
	}
}

func InventoryGeneration(item model.Generation) InventoryGenerationDTO {
	return InventoryGenerationDTO{
		ID: item.ID, ScopeID: item.ScopeID, RunID: item.RunID, State: item.State,
		InventoryReady: item.InventoryReady, GraphReady: item.GraphReady, RoutingReady: item.RoutingReady,
		CreatedAt: item.CreatedAt.UTC(), PublishedAt: inventoryUTCTime(item.PublishedAt),
	}
}

func InventoryRun(item model.SyncRun) InventoryRunDTO {
	return InventoryRunDTO{
		ID: item.ID, ScopeID: item.ScopeID, Status: item.Status, Mode: item.Mode,
		BaseGenerationID: item.BaseGenerationID, GenerationID: item.GenerationID,
		CancelRequestedAt: inventoryUTCTime(item.CancelRequestedAt), CreatedAt: item.CreatedAt.UTC(),
		StartedAt: inventoryUTCTime(item.StartedAt), FinishedAt: inventoryUTCTime(item.FinishedAt),
		ErrorCode: item.ErrorCode,
	}
}

func inventoryUTCTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
