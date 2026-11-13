package service

import (
	"context"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type topologyReadStub struct {
	calls    int
	identity model.EntityIdentity
	query    repository.TopologyNeighborQuery
	err      error
}

func (s *topologyReadStub) Roots(context.Context, string, model.EntityType, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.calls++
	return &repository.TopologyDirectoryPage{}, s.err
}
func (s *topologyReadStub) Children(context.Context, model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.calls++
	return &repository.TopologyDirectoryPage{}, s.err
}

func (s *topologyReadStub) Node(_ context.Context, i model.EntityIdentity) (*repository.TopologyNode, error) {
	s.calls++
	s.identity = i
	return &repository.TopologyNode{SourceID: i.SourceID, EntityType: string(i.EntityType), StableID: i.StableID}, s.err
}
func (s *topologyReadStub) Neighbors(_ context.Context, i model.EntityIdentity, q repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
	s.calls++
	s.identity = i
	s.query = q
	return &repository.TopologyNeighborhood{Limit: q.Limit}, s.err
}
func (s *topologyReadStub) SceneNetwork(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error) {
	panic("unexpected scene network read")
}
func (s *topologyReadStub) SceneNetworkPeers(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error) {
	panic("unexpected network peers read")
}
func (s *topologyReadStub) SceneRoomPower(context.Context, []repository.TopologyNode, int) (*repository.TopologyNeighborhood, error) {
	panic("unexpected room power read")
}
func topologyIdentity() model.EntityIdentity {
	return model.EntityIdentity{SourceID: strings.Repeat("a", 32), EntityType: model.EntityGPU, StableID: "gpu-SN"}
}
func topologyConf() *viper.Viper {
	c := viper.New()
	c.Set("inventory.grants", []map[string]any{{"user_id": "reader", "permissions": []string{"sync:read"}}, {"user_id": "writer", "permissions": []string{"sync:write"}}})
	return c
}
func TestTopologyBrowseAuthorizesBeforeReading(t *testing.T) {
	r := &topologyReadStub{}
	s := NewTopologyBrowseService(r, topologyConf())
	for _, user := range []string{"", "unknown", "writer"} {
		_, err := s.Node(context.Background(), user, topologyIdentity())
		require.ErrorIs(t, err, ErrInventoryForbidden)
		_, err = s.Neighbors(context.Background(), user, topologyIdentity(), repository.TopologyNeighborQuery{})
		require.ErrorIs(t, err, ErrInventoryForbidden)
	}
	require.Zero(t, r.calls)
	node, err := s.Node(context.Background(), "reader", topologyIdentity())
	require.NoError(t, err)
	require.Equal(t, "gpu-SN", node.StableID)
}
func TestTopologyBrowseAnonymousAndDefaults(t *testing.T) {
	c := viper.New()
	c.Set("inventory.auth.enabled", false)
	r := &topologyReadStub{}
	s := NewTopologyBrowseService(r, c)
	_, err := s.Neighbors(context.Background(), InventoryAnonymousUser, topologyIdentity(), repository.TopologyNeighborQuery{})
	require.NoError(t, err)
	require.Equal(t, 100, r.query.Limit)
	require.Equal(t, "both", r.query.Direction)
	_, err = s.Node(context.Background(), "reader", topologyIdentity())
	require.ErrorIs(t, err, ErrInventoryForbidden)
}
func TestTopologyBrowseRejectsInvalidIdentityAndQuery(t *testing.T) {
	r := &topologyReadStub{}
	s := NewTopologyBrowseService(r, topologyConf())
	for _, change := range []func(*model.EntityIdentity){
		func(i *model.EntityIdentity) { i.SourceID = strings.Repeat("A", 32) }, func(i *model.EntityIdentity) { i.SourceID = "a" }, func(i *model.EntityIdentity) { i.EntityType = "unknown" }, func(i *model.EntityIdentity) { i.StableID = "" }, func(i *model.EntityIdentity) { i.StableID = strings.Repeat("x", 257) }, func(i *model.EntityIdentity) { i.EntityType = model.EntityUPS; i.StableID = "01" }, func(i *model.EntityIdentity) { i.EntityType = model.EntityUPS; i.StableID = "0" }, func(i *model.EntityIdentity) { i.EntityType = model.EntityUPS; i.StableID = "9223372036854775808" },
	} {
		i := topologyIdentity()
		change(&i)
		_, err := s.Node(context.Background(), "reader", i)
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	for _, q := range []repository.TopologyNeighborQuery{{Limit: -1}, {Limit: 201}, {Direction: "BOTH"}, {EdgeType: "anything"}} {
		_, err := s.Neighbors(context.Background(), "reader", topologyIdentity(), q)
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	require.Zero(t, r.calls)
	for _, kind := range []model.EntityType{model.EntityDevice, model.EntityInterface, model.EntityGPU, model.EntityPod, model.EntityCabinet, model.EntityDataCenter, model.EntityRPP, model.EntityUPSGroup, model.EntityUPS, model.EntityTransformer} {
		i := topologyIdentity()
		i.EntityType = kind
		i.StableID = "1"
		_, err := s.Node(context.Background(), "reader", i)
		require.NoError(t, err)
	}
}
func TestTopologyBrowseRepositoryErrorsAndUnavailable(t *testing.T) {
	for _, pair := range []struct{ repo, service error }{{repository.ErrInventoryNotFound, ErrInventoryNotFound}, {repository.ErrInventoryNotReady, ErrInventoryNotReady}, {repository.ErrInventoryInvalid, ErrInventoryInvalid}} {
		s := NewTopologyBrowseService(&topologyReadStub{err: pair.repo}, topologyConf())
		_, err := s.Node(context.Background(), "reader", topologyIdentity())
		require.ErrorIs(t, err, pair.service)
		_, err = s.Neighbors(context.Background(), "reader", topologyIdentity(), repository.TopologyNeighborQuery{})
		require.ErrorIs(t, err, pair.service)
	}
	s := NewTopologyBrowseService(nil, topologyConf())
	_, err := s.Node(context.Background(), "reader", topologyIdentity())
	require.ErrorIs(t, err, ErrInventoryNotReady)
}
