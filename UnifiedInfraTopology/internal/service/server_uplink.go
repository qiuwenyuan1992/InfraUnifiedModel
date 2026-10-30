package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
)

type ServerUplinkResult struct {
	Edges            int
	Servers          int
	Skipped          int
	MissingEndpoints int
}

type ServerUplinkWorker struct {
	source *adapter.CMDB
	graph  repository.TopologyGraphRepository
}

func NewServerUplinkWorker(source *adapter.CMDB, graph repository.TopologyGraphRepository) *ServerUplinkWorker {
	return &ServerUplinkWorker{source: source, graph: graph}
}

type serverUplinkCandidate struct {
	identity model.RelationIdentity
	tor      string
}

// Run 先完整采集并检查已有端点，再写边；复读来源一致才返回成功，不删除既有图数据。
func (w *ServerUplinkWorker) Run(ctx context.Context, source model.Source, snapshot time.Time) (ServerUplinkResult, error) {
	result := ServerUplinkResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w == nil || w.source == nil || w.graph == nil || snapshot.IsZero() {
		return result, fmt.Errorf("server uplink requires collector, graph and nonzero snapshot")
	}
	digest, candidates, servers, skipped, err := w.collect(ctx, source, true)
	result.Servers = servers
	result.Skipped = skipped
	if err != nil {
		return result, err
	}
	endpointReader, ok := w.graph.(repository.ServerUplinkEndpointReader)
	if !ok {
		return result, fmt.Errorf("server uplink graph cannot verify ToR ownership")
	}
	existing := map[model.EntityIdentity]bool{}
	checked := map[model.EntityIdentity]bool{}
	valid := make([]model.RelationIdentity, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		missing := false
		for _, endpoint := range []model.EntityIdentity{candidate.identity.From, candidate.identity.To, {SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.tor}} {
			if !checked[endpoint] {
				exists, err := w.graph.VertexExists(ctx, endpoint)
				if err != nil {
					return result, fmt.Errorf("server uplink endpoint read failed: %w", err)
				}
				checked[endpoint] = true
				existing[endpoint] = exists
			}
			if !existing[endpoint] {
				missing = true
			}
		}
		if !missing {
			matches, err := endpointReader.ServerUplinkEndpointsValid(ctx, candidate.identity.From, model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.tor}, candidate.identity.To)
			if err != nil {
				return result, fmt.Errorf("server uplink ownership read failed: %w", err)
			}
			missing = !matches
		}
		if missing {
			result.Skipped++
			result.MissingEndpoints++
			continue
		}
		valid = append(valid, candidate.identity)
	}
	for _, identity := range valid {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		relation := model.Relation{Identity: identity, CreatedAt: snapshot, SyncedAt: snapshot}
		if err := model.ValidateRelation(relation); err != nil {
			return result, err
		}
		if err := w.graph.UpsertRelation(ctx, relation); err != nil {
			return result, fmt.Errorf("server uplink edge write failed (earlier edges may persist): %w", err)
		}
		result.Edges++
	}
	verified, _, secondServers, secondSkipped, err := w.collect(ctx, source, false)
	if err != nil {
		return result, fmt.Errorf("server uplink verification failed (written edges persist): %w", err)
	}
	if verified != digest || secondServers != servers || secondSkipped != skipped {
		return result, fmt.Errorf("server uplink source changed between passes (written edges persist)")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (w *ServerUplinkWorker) collect(ctx context.Context, source model.Source, retain bool) (string, []serverUplinkCandidate, int, int, error) {
	digest := sha256.New()
	candidates := []serverUplinkCandidate{}
	total := -1
	skipped := 0
	var last int64
	identities := map[string]bool{}
	owners := map[string]string{}
	serversSeen := map[string]bool{}
	for start := 0; ; {
		page, err := w.source.CollectServerUplinkPage(ctx, source, start)
		if err != nil {
			return "", nil, max(total, 0), skipped, err
		}
		if total < 0 {
			total = page.Total
		}
		if total != page.Total || page.NextOffset < start || page.NextOffset > total || !page.Done && page.NextOffset == start || page.Done != (page.NextOffset == total) {
			return "", nil, total, skipped, fmt.Errorf("server uplink source pagination changed")
		}
		if page.NextOffset > start {
			if page.FirstInstanceID <= last || page.LastInstanceID < page.FirstInstanceID {
				return "", nil, total, skipped, fmt.Errorf("server uplink server inst_id is not strictly increasing across pages")
			}
			last = page.LastInstanceID
		}
		for _, sn := range page.ServerSNs {
			if sn == "" || serversSeen[sn] {
				return "", nil, total, skipped, fmt.Errorf("server uplink duplicate server SN across pages")
			}
			serversSeen[sn] = true
		}
		skipped += page.Skipped
		header := []any{start, page.NextOffset, page.Total, page.Done, page.Skipped, page.FirstInstanceID, page.LastInstanceID, page.SourceDigest}
		encoded, _ := json.Marshal(header)
		digest.Write(encoded)
		for _, identity := range page.Links {
			id, err := identity.ID()
			if err != nil {
				return "", nil, total, skipped, err
			}
			tor := page.Targets[identity.To.StableID]
			if tor == "" {
				return "", nil, total, skipped, fmt.Errorf("server uplink missing ToR owner metadata")
			}
			if previous, ok := owners[identity.To.StableID]; ok && previous != tor {
				return "", nil, total, skipped, fmt.Errorf("server uplink interface has conflicting ToR owners")
			}
			owners[identity.To.StableID] = tor
			encoded, _ := json.Marshal([]string{id, tor})
			digest.Write(encoded)
			if identities[id] {
				continue
			}
			identities[id] = true
			if retain {
				candidates = append(candidates, serverUplinkCandidate{identity: identity, tor: tor})
			}
		}
		if err := ctx.Err(); err != nil {
			return "", nil, total, skipped, err
		}
		if page.Done {
			break
		}
		start = page.NextOffset
	}
	return hex.EncodeToString(digest.Sum(nil)), candidates, total, skipped, nil
}
