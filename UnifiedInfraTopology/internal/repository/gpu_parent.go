package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"errors"
)

// GPU 节点只允许发布在已存在且已确认分类的 GPU 服务器下。
type GPUParentReader interface {
	ParentGPUDeviceValid(context.Context, model.EntityIdentity) (bool, error)
}

func (unavailableTopologyGraphRepository) ParentGPUDeviceValid(context.Context, model.EntityIdentity) (bool, error) {
	return false, ErrInventoryNotReady
}
func (r *topologyGraphRepository) ParentGPUDeviceValid(ctx context.Context, id model.EntityIdentity) (bool, error) {
	if id.EntityType != model.EntityDevice {
		return false, ErrInventoryConflict
	}
	vid, err := id.VID()
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	query := "MATCH (d:device) WHERE id(d) == $vid RETURN d.device.source_id AS source_id,d.device.device_sn AS device_sn,d.device.parent_type_id AS parent_type_id,d.device.device_type_id AS device_type_id;"
	result, err := r.client.ExecuteParameter(ctx, query, map[string]interface{}{"vid": vid})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil || result == nil || !result.IsSucceed() {
		return false, ErrInventoryNotReady
	}
	if result.GetRowSize() == 0 {
		return false, nil
	}
	if result.GetRowSize() != 1 || result.GetColSize() != 4 {
		return false, errors.New("invalid GPU parent response")
	}
	row, err := result.GetRowValuesByIndex(0)
	if err != nil {
		return false, ErrInventoryNotReady
	}
	for index, expected := range []string{id.SourceID, id.StableID} {
		value, err := row.GetValueByIndex(index)
		if err != nil || !value.IsString() {
			return false, nil
		}
		text, err := value.AsString()
		if err != nil || text != expected {
			return false, nil
		}
	}
	for index, expected := range map[int]int64{2: 52, 3: 58} {
		value, err := row.GetValueByIndex(index)
		if err != nil || !value.IsInt() {
			return false, nil
		}
		integer, err := value.AsInt()
		if err != nil || integer != expected {
			return false, nil
		}
	}
	return true, nil
}
