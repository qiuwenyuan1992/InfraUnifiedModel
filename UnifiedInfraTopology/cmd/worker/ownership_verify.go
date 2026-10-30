package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
)

type ownershipReport struct {
	Total              int64             `json:"total"`
	Current            int64             `json:"current_snapshot"`
	ValidEndpointCount int64             `json:"valid_endpoint_count"`
	Samples            []ownershipSample `json:"samples"`
}
type ownershipSample struct {
	DeviceSN   string    `json:"device_sn"`
	PortUUID   string    `json:"port_uuid"`
	RelationID string    `json:"relation_id"`
	Rank       int64     `json:"rank"`
	SyncedAt   time.Time `json:"synced_at"`
}

func ownershipGraphQuery(current bool) string {
	q := "LOOKUP ON composition_relation WHERE composition_relation.source_id == $source_id AND composition_relation.relation_kind == $relation_kind"
	if current {
		q += " AND composition_relation.synced_at == datetime($snapshot)"
	}
	return q + " YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, composition_relation.relation_id AS relation_id, toString(composition_relation.created_at) AS created_at, toString(composition_relation.synced_at) AS synced_at;"
}

func verifyOwnership(ctx context.Context, client graphclient.Client, sourceID string, snapshot time.Time, expected int64) (*ownershipReport, error) {
	params := map[string]interface{}{"source_id": sourceID, "relation_kind": string(model.RelationOwnsInterface), "snapshot": snapshot.UTC().Format("2006-01-02T15:04:05.000000+00:00")}
	read := func(query string) ([][]string, error) {
		result, err := client.ExecuteParameter(ctx, query, params)
		if err = persistenceCheckGraphResult(result, err); err != nil {
			return nil, err
		}
		return persistenceTable(result)
	}
	total, err := read("LOOKUP ON composition_relation WHERE composition_relation.source_id == $source_id AND composition_relation.relation_kind == $relation_kind YIELD src(edge) AS src | YIELD count(*) AS total;")
	if err != nil {
		return nil, err
	}
	if len(total) != 2 || len(total[1]) != 1 {
		return nil, errors.New("invalid ownership total")
	}
	totalCount, err := strconv.ParseInt(total[1][0], 10, 64)
	if err != nil {
		return nil, err
	}
	current, err := read(ownershipGraphQuery(true))
	if err != nil {
		return nil, err
	}
	r := &ownershipReport{Total: totalCount, Current: int64(len(current) - 1), Samples: []ownershipSample{}}
	if r.Current != expected {
		return r, errors.New("current ownership edge count does not match interface snapshot")
	}
	seen := make(map[string]bool, len(current))
	seenPorts := make(map[string]bool, len(current))
	for _, row := range current[1:] {
		if len(row) != 6 {
			return r, errors.New("invalid ownership edge response")
		}
		rank, err := strconv.ParseInt(row[2], 10, 64)
		if err != nil || rank <= 0 {
			return r, errors.New("invalid ownership edge rank")
		}
		id := persistenceString(row[3])
		created, err := persistenceTime(row[4])
		if err != nil {
			return r, err
		}
		synced, err := persistenceTime(row[5])
		if err != nil || !synced.Equal(snapshot) || created.IsZero() || created.After(synced) {
			return r, errors.New("ownership edge timestamps do not match")
		}
		if seen[id] {
			return r, errors.New("duplicate ownership relation identity")
		}
		seen[id] = true
		portVID := persistenceString(row[1])
		if portVID == "" || seenPorts[portVID] {
			return r, errors.New("physical interface has multiple current owners")
		}
		seenPorts[portVID] = true
	}
	// 标签端点遍历计数与裸边计数对照，避免悬空设备或接口被当作成功。
	prefix := "MATCH (d:device)-[e:composition_relation]->(p:interface) WHERE d.device.source_id == $source_id AND p.interface.source_id == $source_id AND e.source_id == $source_id AND e.relation_kind == $relation_kind AND e.synced_at == datetime($snapshot) AND p.interface.synced_at == datetime($snapshot)"
	matched, err := read(prefix + " RETURN count(e) AS total;")
	if err != nil {
		return r, err
	}
	if len(matched) != 2 || len(matched[1]) != 1 {
		return r, errors.New("invalid ownership endpoint count")
	}
	r.ValidEndpointCount, err = strconv.ParseInt(matched[1][0], 10, 64)
	if err != nil || r.ValidEndpointCount != expected {
		return r, errors.New("ownership edges have missing or mismatched endpoint tags")
	}
	samples, err := read(prefix + " RETURN d.device.device_sn AS device_sn, p.interface.port_uuid AS port_uuid, e.relation_id AS relation_id, rank(e) AS rank, toString(e.synced_at) AS synced_at LIMIT 5;")
	if err != nil {
		return r, err
	}
	for _, row := range samples[1:] {
		if len(row) != 5 {
			return r, errors.New("invalid ownership sample")
		}
		sn, port := persistenceString(row[0]), persistenceString(row[1])
		identity := model.RelationIdentity{SourceID: sourceID, EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityDevice, StableID: sn}, To: model.EntityIdentity{SourceID: sourceID, EntityType: model.EntityInterface, StableID: port}}
		id, err := identity.ID()
		if err != nil {
			return r, err
		}
		rank, err := identity.Rank()
		if err != nil {
			return r, err
		}
		storedRank, err := strconv.ParseInt(row[3], 10, 64)
		if err != nil || rank != storedRank || id != persistenceString(row[2]) {
			return r, errors.New("ownership relation identity or rank mismatch")
		}
		at, err := persistenceTime(row[4])
		if err != nil {
			return r, err
		}
		r.Samples = append(r.Samples, ownershipSample{DeviceSN: sn, PortUUID: port, RelationID: id, Rank: rank, SyncedAt: at})
	}
	return r, nil
}
