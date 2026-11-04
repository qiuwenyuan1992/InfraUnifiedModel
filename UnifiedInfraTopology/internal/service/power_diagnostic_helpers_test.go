package service

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
)

type powerDiagnosticNode struct {
	Entity     model.EntityType `json:"entity"`
	InstanceID int64            `json:"inst_id"`
	Code       string           `json:"code"`
}
type powerDiagnosticEdge struct {
	From   powerDiagnosticNode `json:"from"`
	To     powerDiagnosticNode `json:"to"`
	Reason string              `json:"reason"`
}
type powerDiagnosticCycle struct {
	Classification string                `json:"classification"`
	Nodes          []powerDiagnosticNode `json:"nodes"`
	Edges          []powerDiagnosticEdge `json:"edges"`
}
type powerDiagnosticConflict struct {
	Device                  powerDiagnosticNode `json:"device"`
	Group                   powerDiagnosticNode `json:"ups_group"`
	ExpectedTransformer     powerDiagnosticNode `json:"group_transformer"`
	DeviceTransformer       powerDiagnosticNode `json:"device_transformer"`
	RejectsMainRelationship bool                `json:"rejects_main_relationship"`
}
type powerDiagnosticReference struct {
	State string `json:"state"`
	Value any    `json:"value"`
}
type powerDiagnosticInvalid struct {
	Source    powerDiagnosticNode      `json:"source"`
	Field     string                   `json:"field"`
	Path      string                   `json:"power_path,omitempty"`
	Reference powerDiagnosticReference `json:"reference"`
	Reason    string                   `json:"reason"`
}
type powerDiagnosticReport struct {
	SourceID     string    `json:"source_id"`
	CapturedAt   time.Time `json:"captured_at"`
	SourceDigest string    `json:"source_digest"`
	Objects      int       `json:"objects"`
	Summary      struct {
		Candidates  int            `json:"publishable_candidates"`
		Skipped     int            `json:"skipped"`
		Reasons     map[string]int `json:"reasons"`
		MutualPairs int            `json:"mutual_standby_pairs"`
	} `json:"summary"`
	Cycles               []powerDiagnosticCycle    `json:"cycles"`
	RejectedEdges        []powerDiagnosticEdge     `json:"rejected_standby_edges"`
	StandbyRows          []powerDiagnosticInvalid  `json:"all_standby_references"`
	ConsistencyConflicts []powerDiagnosticConflict `json:"consistency_conflicts"`
	InvalidReferenceRows []powerDiagnosticInvalid  `json:"invalid_reference_rows"`
	EmptyReferenceRows   []powerDiagnosticInvalid  `json:"empty_reference_rows"`
}

func powerDiagnosticNodeOf(entity model.EntityType, r adapter.EntityRecord) powerDiagnosticNode {
	code, _ := r.Properties["code"].(string)
	return powerDiagnosticNode{Entity: entity, InstanceID: r.SourceInstanceID, Code: code}
}
func powerDiagnosticRef(r adapter.EntityRecord, field string) powerDiagnosticReference {
	value, exists := r.Properties[field]
	state := "positive"
	if !exists {
		state = "missing_property"
	} else if value == nil {
		state = "null_or_normalized_empty"
	} else if id, reason := powerReference(value); reason != "" {
		if id == 0 {
			encoded, _ := json.Marshal(value)
			if string(encoded) == "0" {
				state = "zero"
			} else {
				state = "invalid"
			}
		}
	}
	return powerDiagnosticReference{State: state, Value: value}
}
func buildPowerDiagnostic(ctx context.Context, source model.Source, at time.Time, digest string, all map[model.EntityType]*powerRecords, objects int) (*powerDiagnosticReport, error) {
	report := &powerDiagnosticReport{SourceID: source.ID, CapturedAt: at, SourceDigest: digest, Objects: objects, Cycles: []powerDiagnosticCycle{}, RejectedEdges: []powerDiagnosticEdge{}, ConsistencyConflicts: []powerDiagnosticConflict{}, InvalidReferenceRows: []powerDiagnosticInvalid{}, EmptyReferenceRows: []powerDiagnosticInvalid{}}
	result := PowerSyncResult{Reasons: map[string]int{}, ByKind: map[string]int{}}
	candidates, err := powerCandidates(ctx, source, at, all, &result)
	if err != nil {
		return nil, err
	}
	report.Summary.Candidates = len(candidates)
	report.Summary.Skipped = result.Skipped
	report.Summary.Reasons = result.Reasons
	graph := map[int64]int64{}
	nodes := map[int64]powerDiagnosticNode{}
	for _, entity := range powerEntityTypes {
		for _, record := range all[entity].records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			fields := []powerLink{}
			switch entity {
			case model.EntityCabinet:
				fields = []powerLink{{"row_switch_id_a", "A", model.EntityRPP, model.RelationPowerUpstream}, {"row_switch_id_b", "B", model.EntityRPP, model.RelationPowerUpstream}}
			case model.EntityRPP:
				fields = []powerLink{{"ups_group_id", "", model.EntityUPSGroup, model.RelationPowerUpstream}}
			case model.EntityUPS:
				fields = []powerLink{{"ups_group_id", "", model.EntityUPSGroup, model.RelationMemberOf}}
			case model.EntityUPSGroup:
				fields = []powerLink{{"transformer_id_up", "", model.EntityTransformer, model.RelationPowerUpstream}}
			case model.EntityTransformer:
				fields = []powerLink{{"standby_transformer_id", "", model.EntityTransformer, model.RelationHasStandby}}
			}
			for _, field := range fields {
				target, reason := all[field.target].resolve(record.Properties[field.field])
				row := powerDiagnosticInvalid{Source: powerDiagnosticNodeOf(entity, record), Field: field.field, Path: field.path, Reference: powerDiagnosticRef(record, field.field), Reason: reason}
				if entity == model.EntityTransformer {
					report.StandbyRows = append(report.StandbyRows, row)
				}
				if reason == "empty_reference" {
					report.EmptyReferenceRows = append(report.EmptyReferenceRows, row)
				} else if reason != "" {
					report.InvalidReferenceRows = append(report.InvalidReferenceRows, row)
					if entity == model.EntityTransformer {
						report.RejectedEdges = append(report.RejectedEdges, powerDiagnosticEdge{From: row.Source, Reason: reason})
					}
				}
				if reason != "" {
					continue
				}
				if entity == model.EntityTransformer {
					graph[record.SourceInstanceID] = target.SourceInstanceID
					nodes[record.SourceInstanceID] = row.Source
					nodes[target.SourceInstanceID] = powerDiagnosticNodeOf(entity, target)
				}
				if entity == model.EntityUPS || entity == model.EntityRPP {
					checkField := "transformer_id_up"
					if entity == model.EntityRPP {
						checkField = "transformer_group_a_id"
					}
					deviceID, dr := powerReference(record.Properties[checkField])
					groupID, gr := powerReference(target.Properties["transformer_id_up"])
					if dr == "" && gr == "" && deviceID != groupID {
						deviceRecord, _ := all[model.EntityTransformer].resolve(deviceID)
						deviceNode := powerDiagnosticNodeOf(model.EntityTransformer, deviceRecord)
						deviceNode.InstanceID = deviceID
						expectedRecord, _ := all[model.EntityTransformer].resolve(groupID)
						expectedNode := powerDiagnosticNodeOf(model.EntityTransformer, expectedRecord)
						expectedNode.InstanceID = groupID
						report.ConsistencyConflicts = append(report.ConsistencyConflicts, powerDiagnosticConflict{Device: row.Source, Group: powerDiagnosticNodeOf(model.EntityUPSGroup, target), ExpectedTransformer: expectedNode, DeviceTransformer: deviceNode})
					}
				}
			}
		}
	}
	keys := []int64{}
	for key := range graph {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	done := map[int64]bool{}
	for _, start := range keys {
		path := []int64{}
		positions := map[int64]int{}
		for current := start; !done[current]; {
			next, exists := graph[current]
			if !exists {
				break
			}
			if pos, exists := positions[current]; exists {
				cycleIDs := append([]int64(nil), path[pos:]...)
				smallest := 0
				for i, id := range cycleIDs {
					if id < cycleIDs[smallest] {
						smallest = i
					}
				}
				cycleIDs = append(cycleIDs[smallest:], cycleIDs[:smallest]...)
				classification := "long_cycle"
				if len(cycleIDs) == 1 {
					classification = "self"
				} else if len(cycleIDs) == 2 {
					classification = "mutual_pair"
					report.Summary.MutualPairs++
				}
				cycle := powerDiagnosticCycle{Classification: classification}
				for _, id := range cycleIDs {
					reason := "standby_configuration_cycle"
					if len(cycleIDs) == 1 {
						reason = "standby_self_reference"
					} else if len(cycleIDs) == 2 {
						reason = "mutual_pair"
					}
					edge := powerDiagnosticEdge{From: nodes[id], To: nodes[graph[id]], Reason: reason}
					cycle.Nodes = append(cycle.Nodes, nodes[id])
					cycle.Edges = append(cycle.Edges, edge)
					if len(cycleIDs) == 1 {
						report.RejectedEdges = append(report.RejectedEdges, edge)
					}
				}
				report.Cycles = append(report.Cycles, cycle)
				break
			}
			positions[current] = len(path)
			path = append(path, current)
			current = next
		}
		for _, id := range path {
			done[id] = true
		}
	}
	sort.Slice(report.Cycles, func(i, j int) bool {
		return report.Cycles[i].Nodes[0].InstanceID < report.Cycles[j].Nodes[0].InstanceID
	})
	return report, nil
}
