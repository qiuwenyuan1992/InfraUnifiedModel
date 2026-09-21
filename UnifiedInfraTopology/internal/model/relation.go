package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Relation struct {
	Identity   RelationIdentity
	Properties map[string]any
	CreatedAt  time.Time
	SyncedAt   time.Time
}

type RelationSpec struct {
	EdgeType EdgeType
	Kind     RelationKind
	From     EntityType
	To       EntityType
}

var relationSpecs = map[RelationSpec]struct{}{
	{EdgeType: EdgeSpatial, Kind: RelationMemberOf, From: EntityDevice, To: EntityPod}:                {},
	{EdgeType: EdgeSpatial, Kind: RelationLocatedIn, From: EntityDevice, To: EntityCabinet}:           {},
	{EdgeType: EdgeSpatial, Kind: RelationLocatedIn, From: EntityCabinet, To: EntityDataCenter}:       {},
	{EdgeType: EdgeSpatial, Kind: RelationTaggedWith, From: EntityCabinet, To: EntityPod}:             {},
	{EdgeType: EdgeComposition, Kind: RelationOwnsInterface, From: EntityDevice, To: EntityInterface}: {},
	{EdgeType: EdgeComposition, Kind: RelationContainsGPU, From: EntityDevice, To: EntityGPU}:         {},
	{EdgeType: EdgeNetwork, Kind: RelationServerUplink, From: EntityDevice, To: EntityInterface}:      {},
	{EdgeType: EdgeNetwork, Kind: RelationGPUUplink, From: EntityGPU, To: EntityInterface}:            {},
	{EdgeType: EdgeNetwork, Kind: RelationLinksTo, From: EntityInterface, To: EntityInterface}:        {},
	{EdgeType: EdgePower, Kind: RelationPowerUpstream, From: EntityCabinet, To: EntityRPP}:            {},
	{EdgeType: EdgePower, Kind: RelationPowerUpstream, From: EntityRPP, To: EntityUPSGroup}:           {},
	{EdgeType: EdgePower, Kind: RelationMemberOf, From: EntityUPS, To: EntityUPSGroup}:                {},
	{EdgeType: EdgePower, Kind: RelationPowerUpstream, From: EntityUPSGroup, To: EntityTransformer}:   {},
	{EdgeType: EdgePower, Kind: RelationHasStandby, From: EntityTransformer, To: EntityTransformer}:   {},
}

func ValidateRelation(relation Relation) error {
	_, err := NormalizeRelation(relation)
	return err
}

func NormalizeRelation(relation Relation) (Relation, error) {
	if err := relation.Identity.validate(); err != nil {
		return Relation{}, err
	}
	if relation.CreatedAt.IsZero() {
		return Relation{}, errors.New("created time is required")
	}
	if relation.SyncedAt.IsZero() {
		return Relation{}, errors.New("synced time is required")
	}

	spec := RelationSpec{
		EdgeType: relation.Identity.EdgeType,
		Kind:     relation.Identity.Kind,
		From:     relation.Identity.From.EntityType,
		To:       relation.Identity.To.EntityType,
	}
	if _, ok := relationSpecs[spec]; !ok {
		return Relation{}, fmt.Errorf("unsupported relation endpoints: %s/%s %s -> %s", spec.EdgeType, spec.Kind, spec.From, spec.To)
	}

	relation.Properties = cloneProperties(relation.Properties)
	relation.Identity.Discriminator = ""

	switch spec {
	case RelationSpec{EdgeType: EdgeSpatial, Kind: RelationMemberOf, From: EntityDevice, To: EntityPod}:
		plane, err := relationPlane(relation.Properties["plane"])
		if err != nil {
			return Relation{}, err
		}
		relation.Properties["plane"] = plane
		relation.Identity.Discriminator = strconv.FormatInt(plane, 10)
	case RelationSpec{EdgeType: EdgePower, Kind: RelationPowerUpstream, From: EntityCabinet, To: EntityRPP}:
		powerPath, err := normalizedPowerPath(relation.Properties["power_path"])
		if err != nil {
			return Relation{}, err
		}
		relation.Properties["power_path"] = powerPath
		relation.Identity.Discriminator = powerPath
	case RelationSpec{EdgeType: EdgeNetwork, Kind: RelationGPUUplink, From: EntityGPU, To: EntityInterface}:
		if relation.Identity.SourceRelationID == "" {
			return Relation{}, errors.New("GPU uplink source relation ID is required")
		}
		torPort, ok := relation.Properties["tor_port"].(string)
		if !ok || strings.TrimSpace(torPort) == "" {
			return Relation{}, errors.New("GPU uplink tor_port is required")
		}
		relation.Properties["tor_port"] = strings.TrimSpace(torPort)
	case RelationSpec{EdgeType: EdgeNetwork, Kind: RelationLinksTo, From: EntityInterface, To: EntityInterface}:
		fromJSON, err := relation.Identity.From.CanonicalJSON()
		if err != nil {
			return Relation{}, err
		}
		toJSON, err := relation.Identity.To.CanonicalJSON()
		if err != nil {
			return Relation{}, err
		}
		if bytes.Compare(fromJSON, toJSON) > 0 {
			relation.Identity.From, relation.Identity.To = relation.Identity.To, relation.Identity.From
		}
	case RelationSpec{EdgeType: EdgePower, Kind: RelationHasStandby, From: EntityTransformer, To: EntityTransformer}:
		if relation.Identity.From == relation.Identity.To {
			return Relation{}, errors.New("standby transformer cannot reference itself")
		}
	}

	if spec.Kind != RelationGPUUplink && relation.Identity.SourceRelationID != "" {
		return Relation{}, errors.New("source relation ID is only supported for GPU uplinks")
	}
	return relation, nil
}

func cloneProperties(properties map[string]any) map[string]any {
	cloned := make(map[string]any, len(properties))
	for key, value := range properties {
		cloned[key] = value
	}
	return cloned
}

func relationPlane(value any) (int64, error) {
	var plane int64
	switch value := value.(type) {
	case Plane:
		plane = int64(value)
	case int:
		plane = int64(value)
	case int8:
		plane = int64(value)
	case int16:
		plane = int64(value)
	case int32:
		plane = int64(value)
	case int64:
		plane = value
	case uint:
		if uint64(value) > math.MaxInt64 {
			return 0, errors.New("plane is out of range")
		}
		plane = int64(value)
	case uint8:
		plane = int64(value)
	case uint16:
		plane = int64(value)
	case uint32:
		plane = int64(value)
	case uint64:
		if value > math.MaxInt64 {
			return 0, errors.New("plane is out of range")
		}
		plane = int64(value)
	case float64:
		if math.Trunc(value) != value || value > math.MaxInt64 || value < math.MinInt64 {
			return 0, errors.New("plane must be an integer")
		}
		plane = int64(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, errors.New("plane must be an integer")
		}
		plane = parsed
	default:
		return 0, errors.New("plane is required")
	}
	if plane == 0 {
		return 0, errors.New("plane must be non-zero")
	}
	return plane, nil
}

func normalizedPowerPath(value any) (string, error) {
	powerPath, ok := value.(string)
	if !ok {
		return "", errors.New("power_path is required")
	}
	powerPath = strings.ToUpper(strings.TrimSpace(powerPath))
	if powerPath != "A" && powerPath != "B" {
		return "", errors.New("power_path must be A or B")
	}
	return powerPath, nil
}
