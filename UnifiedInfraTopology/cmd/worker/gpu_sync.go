package main

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
)

const gpuSyncResource = "gpu_sync"

type gpuSyncRunner interface {
	Run(context.Context, model.Source, time.Time) (service.GPUSyncResult, error)
}
type gpuSyncRunnerAdapter struct {
	runner gpuSyncRunner
	result service.GPUSyncResult
}

// GPU 检查点记录节点数，而不是 contains_gpu 与 gpu_uplink 边数之和。
func (a *gpuSyncRunnerAdapter) Run(c context.Context, s model.Source, t time.Time) (service.ServerUplinkResult, error) {
	r, e := a.runner.Run(c, s, t)
	a.result = r
	return service.ServerUplinkResult{Edges: r.Nodes, Servers: r.Objects, Skipped: r.Skipped, MissingEndpoints: r.MissingEndpoints}, e
}
func executeGPUSync(c context.Context, control repository.InventoryRepository, runner gpuSyncRunner, run *model.SyncRun, s model.Source, h time.Duration, verify func(context.Context, time.Time, int64) error) (service.GPUSyncResult, error) {
	a := &gpuSyncRunnerAdapter{runner: runner}
	_, err := executeServerUplink(c, standaloneRelationControl{control, gpuSyncResource}, a, run, s, h, verify)
	return a.result, err
}
func runGPUSync(ctx context.Context, conf *viper.Viper, logger *log.Logger, source string, out io.Writer) error {
	var runner *gpuSyncRunnerAdapter
	return runStandaloneRelation(ctx, conf, logger, source, gpuSyncResource, out, func(collector *adapter.CMDB, graph repository.TopologyGraphRepository) serverUplinkRunner {
		runner = &gpuSyncRunnerAdapter{runner: service.NewGPUSyncWorker(collector, graph)}
		return runner
	}, func(c context.Context, client graphclient.Client, t time.Time, n int64) error {
		r, e := verifyGPUGraph(c, client, source, t, n)
		if e == nil && r.ContainsGPU+r.GPUUplink != int64(runner.result.Edges) {
			return errors.New("GPU graph edge count differs from worker result")
		}
		return e
	}, func(a serverUplinkRunner) any {
		r := a.(*gpuSyncRunnerAdapter).result
		return map[string]any{"nodes": r.Nodes, "edges": r.Edges, "objects": r.Objects, "skipped": r.Skipped, "missing_endpoints": r.MissingEndpoints, "reasons": r.Reasons, "by_kind": r.ByKind}
	}, verifyGPUSync)
}

type gpuGraphSample struct {
	PartsSN  string    `json:"parts_sn"`
	VID      string    `json:"vid"`
	ServerSN string    `json:"server_sn"`
	SyncedAt time.Time `json:"synced_at"`
}
type gpuGraphReport struct {
	Total                        int64            `json:"total_nodes"`
	Current                      int64            `json:"current_nodes"`
	ValidEndpointCount           int64            `json:"valid_endpoint_count"`
	ContainsGPU                  int64            `json:"contains_gpu_edges"`
	GPUUplink                    int64            `json:"gpu_uplink_edges"`
	Samples                      []gpuGraphSample `json:"samples"`
	UplinkSourceIdentityVerified bool             `json:"uplink_source_identity_verified"`
	Note                         string           `json:"note"`
}

func verifyGPUGraph(ctx context.Context, client graphclient.Client, source string, at time.Time, expected int64) (*gpuGraphReport, error) {
	read := standaloneGraphReader(ctx, client, source, at)
	scope := "LOOKUP ON gpu WHERE gpu.source_id == $source_id"
	rows, e := read(scope + " YIELD id(vertex) AS vid | YIELD count(*) AS total;")
	if e != nil {
		return nil, e
	}
	total, e := standaloneGraphCount(rows)
	if e != nil {
		return nil, e
	}
	r := &gpuGraphReport{Total: total, Samples: []gpuGraphSample{}, Note: "GPU uplink SourceRelationID is not persisted by the existing graph schema; full source UUID binding and normalized uplink ID cannot be verified from graph alone. Edge counts are reported but the checkpoint persists GPU node count only."}
	if at.IsZero() {
		return r, nil
	}
	nodes, e := read(scope + " AND gpu.synced_at == datetime($snapshot) YIELD id(vertex) AS vid, gpu.parts_sn AS parts_sn, gpu.source_id AS source, toString(gpu.created_at) AS created_at, toString(gpu.synced_at) AS synced_at;")
	if e != nil {
		return r, e
	}
	byVID := map[string]string{}
	bySN := map[string]bool{}
	for _, row := range nodes[1:] {
		if len(row) != 5 {
			return r, errors.New("invalid GPU node response")
		}
		partsSN := persistenceString(row[1])
		identity := model.EntityIdentity{SourceID: source, EntityType: model.EntityGPU, StableID: partsSN}
		vid, e := identity.VID()
		created, e1 := persistenceTime(row[3])
		synced, e2 := persistenceTime(row[4])
		if e != nil || partsSN == "" || bySN[partsSN] || byVID[vid] != "" || persistenceString(row[0]) != vid || persistenceString(row[2]) != source || e1 != nil || e2 != nil || created.IsZero() || created.After(synced) || !synced.Equal(at) {
			return r, errors.New("GPU parts_sn, source, VID, or timestamp mismatch")
		}
		byVID[vid] = partsSN
		bySN[partsSN] = true
	}
	r.Current = int64(len(byVID))
	if r.Current != expected || r.Total < r.Current {
		return r, errors.New("GPU node count differs from checkpoint")
	}
	contains, e := read("LOOKUP ON composition_relation WHERE composition_relation.source_id == $source_id AND composition_relation.relation_kind == \"contains_gpu\" AND composition_relation.synced_at == datetime($snapshot) YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, composition_relation.relation_id AS relation_id, toString(composition_relation.created_at) AS created_at, toString(composition_relation.synced_at) AS synced_at, composition_relation.source_id AS source, composition_relation.relation_kind AS kind;")
	if e != nil {
		return r, e
	}
	raw := map[string][]string{}
	for _, row := range contains[1:] {
		if len(row) != 8 {
			return r, errors.New("invalid contains_gpu response")
		}
		id := persistenceString(row[3])
		rank, e := strconv.ParseInt(row[2], 10, 64)
		created, e1 := persistenceTime(row[4])
		synced, e2 := persistenceTime(row[5])
		if id == "" || raw[id] != nil || e != nil || rank <= 0 || e1 != nil || e2 != nil || created.IsZero() || created.After(synced) || !synced.Equal(at) || persistenceString(row[6]) != source || persistenceString(row[7]) != "contains_gpu" || byVID[persistenceString(row[1])] == "" {
			return r, errors.New("contains_gpu edge identity, endpoint, or snapshot invalid")
		}
		raw[id] = row
	}
	r.ContainsGPU = int64(len(raw))
	if r.ContainsGPU != expected {
		return r, errors.New("each current GPU must have exactly one current contains_gpu edge")
	}
	paths, e := read("MATCH (d:device)-[e:composition_relation]->(g:gpu) WHERE e.source_id == $source_id AND e.relation_kind == \"contains_gpu\" AND e.synced_at == datetime($snapshot) RETURN e.relation_id AS relation_id, rank(e) AS rank, id(d) AS device_vid, id(g) AS gpu_vid, d.device.device_sn AS server_sn, d.device.source_id AS server_source, g.gpu.parts_sn AS gpu_parts_sn, g.gpu.source_id AS gpu_source, d.device.parent_type_id AS server_category, d.device.device_type_id AS server_type;")
	if e != nil {
		return r, e
	}
	matched := map[string]bool{}
	owner := map[string]string{}
	for _, row := range paths[1:] {
		if len(row) != 10 {
			return r, errors.New("invalid GPU ownership path")
		}
		id := persistenceString(row[0])
		edge := raw[id]
		gpuVID := persistenceString(row[3])
		category, e1 := strconv.ParseInt(row[8], 10, 64)
		kind, e2 := strconv.ParseInt(row[9], 10, 64)
		if edge == nil || matched[id] || owner[gpuVID] != "" || e1 != nil || e2 != nil || category != 52 || kind != 58 || persistenceString(row[5]) != source || persistenceString(row[7]) != source || byVID[gpuVID] != persistenceString(row[6]) {
			return r, errors.New("GPU owner must be a unique same-source category 52 type 58 device")
		}
		identity := model.RelationIdentity{SourceID: source, EdgeType: model.EdgeComposition, Kind: model.RelationContainsGPU, From: model.EntityIdentity{SourceID: source, EntityType: model.EntityDevice, StableID: persistenceString(row[4])}, To: model.EntityIdentity{SourceID: source, EntityType: model.EntityGPU, StableID: persistenceString(row[6])}}
		actual, e := identity.ID()
		rank, e3 := identity.Rank()
		from, e4 := identity.From.VID()
		to, e5 := identity.To.VID()
		if e != nil || e3 != nil || e4 != nil || e5 != nil || actual != id || row[1] != strconv.FormatInt(rank, 10) || edge[2] != row[1] || persistenceString(edge[0]) != from || persistenceString(edge[1]) != to || persistenceString(row[2]) != from || gpuVID != to {
			return r, errors.New("contains_gpu identity, rank, or VID mismatch")
		}
		matched[id] = true
		owner[gpuVID] = persistenceString(row[4])
		if len(r.Samples) < 5 {
			r.Samples = append(r.Samples, gpuGraphSample{PartsSN: byVID[gpuVID], VID: gpuVID, ServerSN: owner[gpuVID], SyncedAt: at})
		}
	}
	r.ValidEndpointCount = int64(len(owner))
	if len(matched) != len(raw) || r.ValidEndpointCount != expected {
		return r, errors.New("GPU lacks a unique typed owner path")
	}
	uplinks, e := read("LOOKUP ON network_relation WHERE network_relation.source_id == $source_id AND network_relation.relation_kind == \"gpu_uplink\" AND network_relation.synced_at == datetime($snapshot) YIELD src(edge) AS src, dst(edge) AS dst, rank(edge) AS rank, network_relation.relation_id AS relation_id, network_relation.tor_port AS tor_port, toString(network_relation.created_at) AS created_at, toString(network_relation.synced_at) AS synced_at, network_relation.source_id AS source, network_relation.relation_kind AS kind;")
	if e != nil {
		return r, e
	}
	up := map[string][]string{}
	for _, row := range uplinks[1:] {
		if len(row) != 9 {
			return r, errors.New("invalid GPU uplink edge")
		}
		id := persistenceString(row[3])
		rank, e := strconv.ParseInt(row[2], 10, 64)
		created, e1 := persistenceTime(row[5])
		synced, e2 := persistenceTime(row[6])
		port := persistenceString(row[4])
		// 可验证已存 ID 的格式和派生 rank，但不能恢复未存的来源 UUID。
		decodedID, idErr := hex.DecodeString(id)
		if idErr != nil || len(decodedID) != 32 {
			return r, errors.New("GPU uplink relation ID must be a SHA-256 identity")
		}
		rankValue, rankErr := strconv.ParseUint(id[:16], 16, 64)
		identityRank := int64(rankValue & math.MaxInt64)
		if identityRank == 0 {
			identityRank = 1
		}
		if rankErr != nil || rank != identityRank || up[id] != nil || e != nil || rank <= 0 || port == "" || strings.TrimSpace(port) != port || e1 != nil || e2 != nil || created.IsZero() || created.After(synced) || !synced.Equal(at) || persistenceString(row[7]) != source || persistenceString(row[8]) != "gpu_uplink" || owner[persistenceString(row[0])] == "" {
			return r, errors.New("GPU uplink identity, owner, port, or timestamp invalid")
		}
		up[id] = row
	}
	r.GPUUplink = int64(len(up))
	if len(up) == 0 {
		return r, nil
	}
	// 不筛掉错误分类或来源，所有归属路径都必须逐条通过检查。
	uplinkPaths, e := read("MATCH (g:gpu)-[u:network_relation]->(p:interface)<-[o:composition_relation]-(t:device) WHERE u.source_id == $source_id AND u.relation_kind == \"gpu_uplink\" AND u.synced_at == datetime($snapshot) AND o.relation_kind == \"owns_interface\" RETURN u.relation_id AS relation_id, rank(u) AS rank, id(g) AS gpu_vid, id(p) AS port_vid, g.gpu.parts_sn AS gpu_parts_sn, g.gpu.source_id AS gpu_source, p.interface.port_uuid AS port_uuid, p.interface.source_id AS port_source, p.interface.port_type AS port_type, t.device.device_sn AS owner_sn, t.device.source_id AS owner_source, t.device.parent_type_id AS owner_type, o.source_id AS ownership_source, u.tor_port AS tor_port, p.interface.port_name AS stored_port_name;")
	if e != nil {
		return r, e
	}
	upMatched := map[string]bool{}
	for _, row := range uplinkPaths[1:] {
		if len(row) != 15 {
			return r, errors.New("invalid GPU uplink ownership path")
		}
		id := persistenceString(row[0])
		edge := up[id]
		gpuVID := persistenceString(row[2])
		kind, e := strconv.ParseInt(row[11], 10, 64)
		port := model.EntityIdentity{SourceID: source, EntityType: model.EntityInterface, StableID: persistenceString(row[6])}
		portVID, e1 := port.VID()
		if edge == nil || upMatched[id] || e != nil || e1 != nil || kind != 54 || persistenceString(row[9]) == "" || persistenceString(row[5]) != source || persistenceString(row[7]) != source || persistenceString(row[10]) != source || persistenceString(row[12]) != source || owner[gpuVID] == "" || byVID[gpuVID] != persistenceString(row[4]) || persistenceString(edge[0]) != gpuVID || persistenceString(edge[1]) != portVID || persistenceString(row[3]) != portVID || edge[2] != row[1] || persistenceString(edge[4]) != persistenceString(row[13]) || persistenceString(row[13]) != persistenceString(row[14]) {
			return r, errors.New("GPU uplink must have one same-source ToR owner and valid GPU parent")
		}
		upMatched[id] = true
	}
	if len(upMatched) != len(up) {
		return r, errors.New("GPU uplink lacks a unique typed interface ownership path")
	}
	return r, nil
}
func verifyGPUSync(ctx context.Context, conf *viper.Viper, source, run string, out io.Writer) error {
	return verifyStandaloneRelation(ctx, conf, source, run, gpuSyncResource, out, func(c context.Context, client graphclient.Client, t time.Time, n int64) (any, int64, int64, error) {
		r, e := verifyGPUGraph(c, client, source, t, n)
		if e != nil {
			return r, 0, 0, e
		}
		return r, r.Current, r.ValidEndpointCount, nil
	})
}
