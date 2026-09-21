package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testEntity(entityType EntityType, stableID string) EntityIdentity {
	return EntityIdentity{SourceID: "cmdb-primary", EntityType: entityType, StableID: stableID}
}

func testRelation(edgeType EdgeType, kind RelationKind, fromType, toType EntityType) Relation {
	now := time.Now().UTC().Truncate(time.Second)
	return Relation{
		Identity: RelationIdentity{
			SourceID: "cmdb-primary",
			EdgeType: edgeType,
			Kind:     kind,
			From:     testEntity(fromType, string(fromType)+"-from"),
			To:       testEntity(toType, string(toType)+"-to"),
		},
		CreatedAt: now,
		SyncedAt:  now,
	}
}

func TestRelationMatrixAcceptsAllSupportedEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		edgeType   EdgeType
		kind       RelationKind
		from       EntityType
		to         EntityType
		properties map[string]any
		sourceID   string
	}{
		{"device member of pod", EdgeSpatial, RelationMemberOf, EntityDevice, EntityPod, map[string]any{"plane": int64(1)}, ""},
		{"device located in cabinet", EdgeSpatial, RelationLocatedIn, EntityDevice, EntityCabinet, nil, ""},
		{"cabinet located in data center", EdgeSpatial, RelationLocatedIn, EntityCabinet, EntityDataCenter, nil, ""},
		{"cabinet tagged with pod", EdgeSpatial, RelationTaggedWith, EntityCabinet, EntityPod, nil, ""},
		{"device owns interface", EdgeComposition, RelationOwnsInterface, EntityDevice, EntityInterface, nil, ""},
		{"device contains gpu", EdgeComposition, RelationContainsGPU, EntityDevice, EntityGPU, nil, ""},
		{"device server uplink", EdgeNetwork, RelationServerUplink, EntityDevice, EntityInterface, nil, ""},
		{"gpu uplink", EdgeNetwork, RelationGPUUplink, EntityGPU, EntityInterface, map[string]any{"tor_port": "25GE1/0/26"}, "uplink-1"},
		{"interfaces link", EdgeNetwork, RelationLinksTo, EntityInterface, EntityInterface, nil, ""},
		{"cabinet power upstream", EdgePower, RelationPowerUpstream, EntityCabinet, EntityRPP, map[string]any{"power_path": "A"}, ""},
		{"rpp power upstream", EdgePower, RelationPowerUpstream, EntityRPP, EntityUPSGroup, nil, ""},
		{"ups member of group", EdgePower, RelationMemberOf, EntityUPS, EntityUPSGroup, nil, ""},
		{"ups group power upstream", EdgePower, RelationPowerUpstream, EntityUPSGroup, EntityTransformer, nil, ""},
		{"transformer standby", EdgePower, RelationHasStandby, EntityTransformer, EntityTransformer, nil, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relation := testRelation(test.edgeType, test.kind, test.from, test.to)
			relation.Properties = test.properties
			relation.Identity.SourceRelationID = test.sourceID
			if test.kind == RelationHasStandby {
				relation.Identity.To.StableID = "standby-transformer"
			}
			require.NoError(t, ValidateRelation(relation))
		})
	}
}

func TestValidateRelationRejectsInvalidRelations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Relation)
	}{
		{"reversed direction", func(r *Relation) { r.Identity.From, r.Identity.To = r.Identity.To, r.Identity.From }},
		{"wrong edge type", func(r *Relation) { r.Identity.EdgeType = EdgeNetwork }},
		{"unknown relation kind", func(r *Relation) { r.Identity.Kind = RelationKind("unknown") }},
		{"missing plane", func(r *Relation) { delete(r.Properties, "plane") }},
		{"missing created time", func(r *Relation) { r.CreatedAt = time.Time{} }},
		{"missing synced time", func(r *Relation) { r.SyncedAt = time.Time{} }},
		{"different source", func(r *Relation) { r.Identity.To.SourceID = "cmdb-secondary" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relation := testRelation(EdgeSpatial, RelationMemberOf, EntityDevice, EntityPod)
			relation.Properties = map[string]any{"plane": int64(1)}
			test.mutate(&relation)
			require.Error(t, ValidateRelation(relation))
		})
	}

	power := testRelation(EdgePower, RelationPowerUpstream, EntityCabinet, EntityRPP)
	require.Error(t, ValidateRelation(power))

	gpuUplink := testRelation(EdgeNetwork, RelationGPUUplink, EntityGPU, EntityInterface)
	gpuUplink.Properties = map[string]any{"tor_port": "25GE1/0/26"}
	require.Error(t, ValidateRelation(gpuUplink))
	gpuUplink.Identity.SourceRelationID = "uplink-1"
	gpuUplink.Properties["tor_port"] = ""
	require.Error(t, ValidateRelation(gpuUplink))

	standby := testRelation(EdgePower, RelationHasStandby, EntityTransformer, EntityTransformer)
	standby.Identity.To = standby.Identity.From
	require.Error(t, ValidateRelation(standby))
}

func TestNormalizeRelationRejectsPropertiesForWrongKind(t *testing.T) {
	tests := []Relation{
		testRelation(EdgeSpatial, RelationLocatedIn, EntityDevice, EntityCabinet),
		testRelation(EdgePower, RelationPowerUpstream, EntityRPP, EntityUPSGroup),
		testRelation(EdgeNetwork, RelationServerUplink, EntityDevice, EntityInterface),
		testRelation(EdgeNetwork, RelationLinksTo, EntityInterface, EntityInterface),
	}
	properties := []map[string]any{
		{"plane": int64(1)},
		{"power_path": "A"},
		{"gpu_ip": "192.0.2.10"},
		{"tor_port": "Ethernet1/1"},
	}
	for index := range tests {
		tests[index].Properties = properties[index]
		require.ErrorContains(t, ValidateRelation(tests[index]), "unsupported relation property")
	}
}

func TestNormalizeRelationCanonicalizesLinksTo(t *testing.T) {
	left := testEntity(EntityInterface, "port-a")
	right := testEntity(EntityInterface, "port-z")

	forward := testRelation(EdgeNetwork, RelationLinksTo, EntityInterface, EntityInterface)
	forward.Identity.From = right
	forward.Identity.To = left

	reverse := forward
	reverse.Identity.From = left
	reverse.Identity.To = right

	normalizedForward, err := NormalizeRelation(forward)
	require.NoError(t, err)
	normalizedReverse, err := NormalizeRelation(reverse)
	require.NoError(t, err)
	require.Equal(t, left, normalizedForward.Identity.From)
	require.Equal(t, right, normalizedForward.Identity.To)
	require.Empty(t, normalizedForward.Properties)

	forwardID, err := normalizedForward.Identity.ID()
	require.NoError(t, err)
	reverseID, err := normalizedReverse.Identity.ID()
	require.NoError(t, err)
	require.Equal(t, forwardID, reverseID)
}

func TestNormalizeRelationSetsBusinessDiscriminator(t *testing.T) {
	memberOf := testRelation(EdgeSpatial, RelationMemberOf, EntityDevice, EntityPod)
	memberOf.Properties = map[string]any{"plane": int64(9)}
	normalized, err := NormalizeRelation(memberOf)
	require.NoError(t, err)
	require.Equal(t, "9", normalized.Identity.Discriminator)
	require.Equal(t, int64(9), normalized.Properties["plane"])

	power := testRelation(EdgePower, RelationPowerUpstream, EntityCabinet, EntityRPP)
	power.Properties = map[string]any{"power_path": " b "}
	normalized, err = NormalizeRelation(power)
	require.NoError(t, err)
	require.Equal(t, "B", normalized.Identity.Discriminator)
	require.Equal(t, "B", normalized.Properties["power_path"])
}
