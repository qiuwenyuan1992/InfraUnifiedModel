package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"errors"
)

type LinksToEndpointReader interface {
	LinksToEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error)
}

func (unavailableTopologyGraphRepository) LinksToEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error) {
	return false, ErrInventoryNotReady
}
func (r *topologyGraphRepository) LinksToEndpointsValid(ctx context.Context, fromDevice, toDevice, fromPort, toPort model.EntityIdentity) (bool, error) {
	ids := []model.EntityIdentity{fromDevice, toDevice, fromPort, toPort}
	for index, id := range ids {
		kind := model.EntityDevice
		if index >= 2 {
			kind = model.EntityInterface
		}
		if id.EntityType != kind || id.SourceID != fromDevice.SourceID {
			return false, ErrInventoryConflict
		}
		if _, err := id.VID(); err != nil {
			return false, err
		}
	}
	if fromPort == toPort {
		return false, ErrInventoryConflict
	}
	for index, device := range []model.EntityIdentity{fromDevice, toDevice} {
		port := []model.EntityIdentity{fromPort, toPort}[index]
		if err := ctx.Err(); err != nil {
			return false, err
		}
		vid, _ := port.VID()
		query := `MATCH (t:device)-[o:composition_relation]->(p:interface) WHERE id(p) == $port_vid AND o.source_id == $source_id AND o.relation_kind == "owns_interface" RETURN p.interface.source_id AS port_source, p.interface.port_uuid AS port_uuid, p.interface.port_type AS port_type, t.device.device_sn AS device_sn, t.device.parent_type_id AS device_type, t.device.source_id AS owner_source, t.device.device_sn AS owner_sn, t.device.parent_type_id AS owner_type, p.interface.port_uuid AS owner_port_uuid, p.interface.port_type AS owner_port_type;`
		result, err := r.client.ExecuteParameter(ctx, query, map[string]interface{}{"port_vid": vid, "source_id": port.SourceID})
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil || result == nil || !result.IsSucceed() {
			return false, ErrInventoryNotReady
		}
		if result.GetColSize() != 10 {
			return false, errors.New("invalid links_to endpoint columns")
		}
		if result.GetRowSize() != 1 {
			return false, nil
		}
		row, err := result.GetRowValuesByIndex(0)
		if err != nil {
			return false, ErrInventoryNotReady
		}
		for col, expected := range map[int]string{0: port.SourceID, 1: port.StableID, 2: "physics", 3: device.StableID, 5: device.SourceID, 6: device.StableID, 8: port.StableID, 9: "physics"} {
			v, err := row.GetValueByIndex(col)
			if err != nil || !v.IsString() {
				return false, nil
			}
			text, err := v.AsString()
			if err != nil || text != expected {
				return false, nil
			}
		}
		for _, col := range []int{4, 7} {
			v, err := row.GetValueByIndex(col)
			if err != nil || !v.IsInt() {
				return false, nil
			}
			category, err := v.AsInt()
			if err != nil || category != 54 {
				return false, nil
			}
		}
	}
	return true, nil
}
