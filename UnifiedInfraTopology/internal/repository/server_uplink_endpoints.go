package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"errors"
)

type ServerUplinkEndpointReader interface {
	ServerUplinkEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error)
}

func (unavailableTopologyGraphRepository) ServerUplinkEndpointsValid(context.Context, model.EntityIdentity, model.EntityIdentity, model.EntityIdentity) (bool, error) {
	return false, ErrInventoryNotReady
}

func serverUplinkEndpointQuery(server, tor, port model.EntityIdentity) (string, map[string]interface{}, error) {
	if server.EntityType != model.EntityDevice || tor.EntityType != model.EntityDevice || port.EntityType != model.EntityInterface || server.SourceID != tor.SourceID || server.SourceID != port.SourceID {
		return "", nil, ErrInventoryConflict
	}
	serverVID, err := server.VID()
	if err != nil {
		return "", nil, err
	}
	if _, err := tor.VID(); err != nil {
		return "", nil, err
	}
	portVID, err := port.VID()
	if err != nil {
		return "", nil, err
	}
	// 返回该接口的全部同来源归属，不把 ToR SN 放进 WHERE，否则无法发现多归属。
	query := "MATCH (s:device), (t:device)-[o:composition_relation]->(p:interface) WHERE id(s) == $server_vid AND id(p) == $port_vid AND s.device.source_id == $source_id AND p.interface.source_id == $source_id AND o.source_id == $source_id AND o.relation_kind == \"owns_interface\" RETURN s.device.device_sn AS server_sn, s.device.parent_type_id AS server_type, t.device.source_id AS tor_source, t.device.device_sn AS tor_sn, t.device.parent_type_id AS tor_type, p.interface.port_uuid AS port_uuid, p.interface.port_type AS port_type;"
	return query, map[string]interface{}{"source_id": server.SourceID, "server_vid": serverVID, "port_vid": portVID}, nil
}

func (r *topologyGraphRepository) ServerUplinkEndpointsValid(ctx context.Context, server, tor, port model.EntityIdentity) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	query, params, err := serverUplinkEndpointQuery(server, tor, port)
	if err != nil {
		return false, err
	}
	result, err := r.client.ExecuteParameter(ctx, query, params)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil || result == nil || !result.IsSucceed() {
		return false, ErrInventoryNotReady
	}
	if result.GetRowSize() != 1 {
		return false, nil
	}
	if result.GetColSize() != 7 {
		return false, errors.New("invalid uplink endpoint response")
	}
	row, err := result.GetRowValuesByIndex(0)
	if err != nil {
		return false, errors.New("invalid uplink endpoint row")
	}
	expectedText := map[int]string{0: server.StableID, 2: tor.SourceID, 3: tor.StableID, 5: port.StableID, 6: "physics"}
	for index, expected := range expectedText {
		value, err := row.GetValueByIndex(index)
		if err != nil || !value.IsString() {
			return false, nil
		}
		text, err := value.AsString()
		if err != nil || text != expected {
			return false, nil
		}
	}
	for index, expected := range map[int]int64{1: 52, 4: 54} {
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
