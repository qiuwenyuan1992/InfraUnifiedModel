package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/stretchr/testify/require"
)

type topologyDirectoryStub struct {
	topologyReadStub
	directoryQuery repository.TopologyDirectoryQuery
	page           *repository.TopologyDirectoryPage
}

func (s *topologyDirectoryStub) Roots(_ context.Context, _ string, _ model.EntityType, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.calls++
	s.directoryQuery = q
	return s.page, s.err
}
func (s *topologyDirectoryStub) Children(_ context.Context, i model.EntityIdentity, q repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	s.identity = i
	return s.Roots(context.Background(), i.SourceID, i.EntityType, q)
}

func TestTopologyDirectorySignedCursorBinding(t *testing.T) {
	c := topologyConf()
	c.Set("inventory.cursor_key", strings.Repeat("k", 32))
	r := &topologyDirectoryStub{page: &repository.TopologyDirectoryPage{Items: []repository.TopologyNode{}, NextAfter: strings.Repeat("b", 64)}}
	s := NewTopologyBrowseService(r, c)
	p, err := s.Roots(context.Background(), "reader", strings.Repeat("a", 32), model.EntityPod, repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	require.Equal(t, 50, r.directoryQuery.Limit)
	require.NotEqual(t, r.page.NextAfter, p.NextAfter)
	cursor := p.NextAfter
	_, err = s.Roots(context.Background(), "reader", strings.Repeat("a", 32), model.EntityPod, repository.TopologyDirectoryQuery{Limit: 100, After: cursor})
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("b", 64), r.directoryQuery.After)
	for _, q := range []repository.TopologyDirectoryQuery{{After: strings.Repeat("b", 64)}, {After: cursor + "x"}, {Limit: 101}} {
		_, err = s.Roots(context.Background(), "reader", strings.Repeat("a", 32), model.EntityPod, q)
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	_, err = s.Roots(context.Background(), "reader", strings.Repeat("c", 32), model.EntityPod, repository.TopologyDirectoryQuery{After: cursor})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	_, err = s.Roots(context.Background(), "reader", strings.Repeat("a", 32), model.EntityDataCenter, repository.TopologyDirectoryQuery{After: cursor})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	i := model.EntityIdentity{SourceID: strings.Repeat("a", 32), EntityType: model.EntityPod, StableID: "parent"}
	p, err = s.Children(context.Background(), "reader", i, repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	i.StableID = "other"
	_, err = s.Children(context.Background(), "reader", i, repository.TopologyDirectoryQuery{After: p.NextAfter})
	require.ErrorIs(t, err, ErrInventoryInvalid)
}

func TestTopologyDirectoryAuthorizationAndValidation(t *testing.T) {
	r := &topologyDirectoryStub{page: &repository.TopologyDirectoryPage{}}
	c := topologyConf()
	c.Set("inventory.cursor_key", strings.Repeat("k", 32))
	s := NewTopologyBrowseService(r, c)
	_, err := s.Roots(context.Background(), "writer", "bad", model.EntityDevice, repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryForbidden)
	_, err = s.Roots(context.Background(), "reader", strings.Repeat("a", 32), model.EntityDevice, repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	_, err = s.Children(context.Background(), "reader", topologyIdentity(), repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	require.Zero(t, r.calls)
	r.err = errors.New("backend failure")
	_, err = s.Roots(context.Background(), "reader", strings.Repeat("a", 32), model.EntityPod, repository.TopologyDirectoryQuery{})
	require.ErrorIs(t, err, ErrInventoryNotReady)
}

func TestTopologyDirectoryCursorVIDAndNamespace(t *testing.T) {
	c := topologyConf()
	c.Set("inventory.grants", []map[string]any{{"user_id": "reader", "permissions": []string{"sync:read"}}, {"user_id": "other", "permissions": []string{"sync:read"}}})
	c.Set("inventory.cursor_key", strings.Repeat("k", 32))
	r := &topologyDirectoryStub{page: &repository.TopologyDirectoryPage{}}
	s := NewTopologyBrowseService(r, c).(*topologyBrowseService)
	binding := topologyDirectoryCursor{Version: 1, User: "reader", Source: strings.Repeat("a", 32), EntityType: model.EntityPod}
	for _, after := range []string{strings.Repeat("b", 32), strings.Repeat("B", 64), strings.Repeat("z", 64), "", strings.Repeat("b", 65)} {
		cursor := binding
		cursor.After = after
		data, err := json.Marshal(cursor)
		require.NoError(t, err)
		payload := base64.RawURLEncoding.EncodeToString(data)
		token := payload + "." + base64.RawURLEncoding.EncodeToString(s.directorySignature(payload))
		_, err = s.Roots(context.Background(), "reader", binding.Source, binding.EntityType, repository.TopologyDirectoryQuery{After: token})
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	legacy := s.auth.encodeCursor(inventoryCursor{Version: 1, UserID: "reader", LastID: strings.Repeat("b", 32)})
	_, err := s.Roots(context.Background(), "reader", binding.Source, binding.EntityType, repository.TopologyDirectoryQuery{After: legacy})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	_, err = s.Roots(context.Background(), "reader", binding.Source, binding.EntityType, repository.TopologyDirectoryQuery{After: strings.Repeat("x", 4097)})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	require.Zero(t, r.calls)
	r.page.NextAfter = strings.Repeat("b", 64)
	p, err := s.Roots(context.Background(), "reader", binding.Source, binding.EntityType, repository.TopologyDirectoryQuery{})
	require.NoError(t, err)
	_, err = s.Roots(context.Background(), "other", binding.Source, binding.EntityType, repository.TopologyDirectoryQuery{After: p.NextAfter})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	require.Equal(t, 1, r.calls)
}
