package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTopologyEnumsAreClosed(t *testing.T) {
	for _, entityType := range []EntityType{
		EntityDevice,
		EntityInterface,
		EntityGPU,
		EntityPod,
		EntityCabinet,
		EntityDataCenter,
		EntityRPP,
		EntityUPSGroup,
		EntityUPS,
		EntityTransformer,
	} {
		require.True(t, entityType.Valid(), entityType)
	}
	require.False(t, EntityType("address").Valid())

	for _, edgeType := range []EdgeType{
		EdgeSpatial,
		EdgeComposition,
		EdgeNetwork,
		EdgePower,
	} {
		require.True(t, edgeType.Valid(), edgeType)
	}
	require.False(t, EdgeType("owns_address").Valid())

	for _, kind := range []RelationKind{
		RelationMemberOf,
		RelationLocatedIn,
		RelationTaggedWith,
		RelationOwnsInterface,
		RelationContainsGPU,
		RelationServerUplink,
		RelationGPUUplink,
		RelationLinksTo,
		RelationPowerUpstream,
		RelationHasStandby,
	} {
		require.True(t, kind.Valid(), kind)
	}
	require.False(t, RelationKind("unknown").Valid())
}

func TestParsePlanePreservesUnknownValue(t *testing.T) {
	for _, value := range []int64{1, 2, 3, 4} {
		plane, canonical := ParsePlane(value)
		require.Equal(t, Plane(value), plane)
		require.True(t, canonical)
	}

	plane, canonical := ParsePlane(9)
	require.Equal(t, Plane(9), plane)
	require.False(t, canonical)
}

func TestEntityIdentityCanonicalJSON(t *testing.T) {
	identity := EntityIdentity{
		SourceID:   "cmdb-primary",
		EntityType: EntityDevice,
		StableID:   "SN123456",
	}

	canonical, err := identity.CanonicalJSON()
	require.NoError(t, err)
	require.Equal(t, `["cmdb-primary","device","SN123456"]`, string(canonical))
}

func TestEntityIdentityVIDIsDeterministicAndIsolated(t *testing.T) {
	identity := EntityIdentity{
		SourceID:   "cmdb-primary",
		EntityType: EntityDevice,
		StableID:   "SN123456",
	}

	vid, err := identity.VID()
	require.NoError(t, err)
	require.Equal(t, "046c883dfe6375ca4f441ff2cff91203a162cf5269dd31e67cd4036011825977", vid)
	require.Len(t, vid, 64)
	require.Equal(t, strings.ToLower(vid), vid)

	repeated, err := identity.VID()
	require.NoError(t, err)
	require.Equal(t, vid, repeated)

	otherSource := identity
	otherSource.SourceID = "cmdb-secondary"
	otherSourceVID, err := otherSource.VID()
	require.NoError(t, err)
	require.NotEqual(t, vid, otherSourceVID)

	otherType := identity
	otherType.EntityType = EntityGPU
	otherTypeVID, err := otherType.VID()
	require.NoError(t, err)
	require.NotEqual(t, vid, otherTypeVID)
}

func TestEntityIdentityRejectsInvalidFields(t *testing.T) {
	tests := []EntityIdentity{
		{EntityType: EntityDevice, StableID: "SN123456"},
		{SourceID: "cmdb-primary", StableID: "SN123456"},
		{SourceID: "cmdb-primary", EntityType: EntityType("address"), StableID: "10.0.0.1"},
		{SourceID: "cmdb-primary", EntityType: EntityDevice},
	}
	for _, identity := range tests {
		_, err := identity.CanonicalJSON()
		require.Error(t, err)
		_, err = identity.VID()
		require.Error(t, err)
	}
}

func TestRelationIdentityUsesSourceRelationUUID(t *testing.T) {
	identity := RelationIdentity{
		SourceID:         "cmdb-primary",
		EdgeType:         EdgeNetwork,
		Kind:             RelationGPUUplink,
		From:             EntityIdentity{SourceID: "cmdb-primary", EntityType: EntityGPU, StableID: "gpu-1"},
		To:               EntityIdentity{SourceID: "cmdb-primary", EntityType: EntityInterface, StableID: "port-1"},
		SourceRelationID: "uplink-uuid",
	}

	relationID, err := identity.ID()
	require.NoError(t, err)
	require.Equal(t, "11e1ed1f1f7a7e17dad67ae17a7d2e5d695a0177bc8103438810c6023703b85c", relationID)

	rank, err := identity.Rank()
	require.NoError(t, err)
	require.Equal(t, int64(0x11e1ed1f1f7a7e17), rank)
}

func TestRelationIdentityUsesEndpointIdentitiesAndDiscriminator(t *testing.T) {
	identity := RelationIdentity{
		SourceID:      "cmdb-primary",
		EdgeType:      EdgeSpatial,
		Kind:          RelationMemberOf,
		From:          EntityIdentity{SourceID: "cmdb-primary", EntityType: EntityDevice, StableID: "SN123456"},
		To:            EntityIdentity{SourceID: "cmdb-primary", EntityType: EntityPod, StableID: "pod-1"},
		Discriminator: "2",
	}

	relationID, err := identity.ID()
	require.NoError(t, err)
	require.Equal(t, "272867adad59929e5b1f67e58e645fe61e5d6fa3a44c3c5b3ed5e8491a07a0e0", relationID)
}

func TestRelationIdentityRejectsInvalidFields(t *testing.T) {
	valid := RelationIdentity{
		SourceID: "cmdb-primary",
		EdgeType: EdgeSpatial,
		Kind:     RelationLocatedIn,
		From:     EntityIdentity{SourceID: "cmdb-primary", EntityType: EntityDevice, StableID: "SN123456"},
		To:       EntityIdentity{SourceID: "cmdb-primary", EntityType: EntityCabinet, StableID: "cabinet-1"},
	}

	tests := []RelationIdentity{
		{},
		func() RelationIdentity { value := valid; value.SourceID = ""; return value }(),
		func() RelationIdentity { value := valid; value.EdgeType = EdgeType("invalid"); return value }(),
		func() RelationIdentity { value := valid; value.Kind = RelationKind("invalid"); return value }(),
		func() RelationIdentity { value := valid; value.From.SourceID = "other"; return value }(),
		func() RelationIdentity { value := valid; value.To.StableID = ""; return value }(),
	}
	for _, identity := range tests {
		_, err := identity.ID()
		require.Error(t, err)
		_, err = identity.Rank()
		require.Error(t, err)
	}
}

func TestRankFromRelationID(t *testing.T) {
	rank, err := rankFromRelationID(strings.Repeat("0", 64))
	require.NoError(t, err)
	require.EqualValues(t, 1, rank)

	_, err = rankFromRelationID("not-a-relation-id")
	require.Error(t, err)
}
