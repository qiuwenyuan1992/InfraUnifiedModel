package model

type EntityType string

const (
	EntityDevice      EntityType = "device"
	EntityInterface   EntityType = "interface"
	EntityGPU         EntityType = "gpu"
	EntityPod         EntityType = "pod"
	EntityCabinet     EntityType = "cabinet"
	EntityDataCenter  EntityType = "data_center"
	EntityRPP         EntityType = "rpp"
	EntityUPSGroup    EntityType = "ups_group"
	EntityUPS         EntityType = "ups"
	EntityTransformer EntityType = "transformer"
)

func (t EntityType) Valid() bool {
	switch t {
	case EntityDevice,
		EntityInterface,
		EntityGPU,
		EntityPod,
		EntityCabinet,
		EntityDataCenter,
		EntityRPP,
		EntityUPSGroup,
		EntityUPS,
		EntityTransformer:
		return true
	default:
		return false
	}
}

type EdgeType string

const (
	EdgeSpatial     EdgeType = "spatial_relation"
	EdgeComposition EdgeType = "composition_relation"
	EdgeNetwork     EdgeType = "network_relation"
	EdgePower       EdgeType = "power_relation"
)

func (t EdgeType) Valid() bool {
	switch t {
	case EdgeSpatial, EdgeComposition, EdgeNetwork, EdgePower:
		return true
	default:
		return false
	}
}

type RelationKind string

const (
	RelationMemberOf      RelationKind = "member_of"
	RelationLocatedIn     RelationKind = "located_in"
	RelationTaggedWith    RelationKind = "tagged_with"
	RelationOwnsInterface RelationKind = "owns_interface"
	RelationContainsGPU   RelationKind = "contains_gpu"
	RelationServerUplink  RelationKind = "server_uplink"
	RelationGPUUplink     RelationKind = "gpu_uplink"
	RelationLinksTo       RelationKind = "links_to"
	RelationPowerUpstream RelationKind = "power_upstream"
	RelationHasStandby    RelationKind = "has_standby"
)

func (k RelationKind) Valid() bool {
	switch k {
	case RelationMemberOf,
		RelationLocatedIn,
		RelationTaggedWith,
		RelationOwnsInterface,
		RelationContainsGPU,
		RelationServerUplink,
		RelationGPUUplink,
		RelationLinksTo,
		RelationPowerUpstream,
		RelationHasStandby:
		return true
	default:
		return false
	}
}

type Plane int64

const (
	PlaneManagement Plane = 1
	PlaneCompute    Plane = 2
	PlaneStorage    Plane = 3
	PlaneOutOfBand  Plane = 4
)

func ParsePlane(value int64) (Plane, bool) {
	plane := Plane(value)
	switch plane {
	case PlaneManagement, PlaneCompute, PlaneStorage, PlaneOutOfBand:
		return plane, true
	default:
		return plane, false
	}
}
