package repository

import (
	"context"
	"sort"
	"strings"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

func TestTopologyDirectoryRootsSourceAndPaging(t *testing.T) {
	for _, entityType := range []model.EntityType{model.EntityDataCenter, model.EntityPod} {
		t.Run(string(entityType), func(t *testing.T) {
			source := "source-'quoted"
			identities := []model.EntityIdentity{{SourceID: source, EntityType: entityType, StableID: "a"}, {SourceID: source, EntityType: entityType, StableID: "b"}, {SourceID: source, EntityType: entityType, StableID: "c"}}
			sort.Slice(identities, func(i, j int) bool { a, _ := identities[i].VID(); b, _ := identities[j].VID(); return a < b })
			rows := [][]*nebula.Value{}
			for _, identity := range identities {
				rows = append(rows, []*nebula.Value{readPeer(t, identity)})
			}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"node"}, rows...)}}
			repo := &topologyReadRepository{client: fake}
			page, err := repo.Roots(context.Background(), source, entityType, TopologyDirectoryQuery{Limit: 2})
			require.NoError(t, err)
			require.Len(t, page.Items, 2)
			require.Equal(t, page.Items[1].ID, page.NextAfter)
			require.Less(t, page.Items[0].ID, page.Items[1].ID)
			call := fake.calls[0]
			require.Contains(t, call.statement, "MATCH (n:"+string(entityType)+")")
			require.Contains(t, call.statement, "n."+string(entityType)+".source_id == $source_id")
			require.Contains(t, call.statement, "id(n) > $after")
			require.Contains(t, call.statement, "ORDER BY vid LIMIT 3")
			require.NotContains(t, call.statement, source)
			require.Equal(t, source, call.params["source_id"])
			require.Equal(t, "", call.params["after"])
			after := page.NextAfter
			fake.results = []*nebulago.ResultSet{readResult(t, []string{"node"}, rows[2])}
			page, err = repo.Roots(context.Background(), source, entityType, TopologyDirectoryQuery{Limit: 2, After: after})
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Empty(t, page.NextAfter)
			require.Equal(t, after, fake.calls[1].params["after"])
			require.NotContains(t, fake.calls[1].statement, after)
		})
	}
}

func TestTopologyDirectoryChildrenFixedMatrixDistinctBeforeLimit(t *testing.T) {
	for _, tc := range []struct {
		parent, child model.EntityType
		kind          model.RelationKind
	}{
		{model.EntityDataCenter, model.EntityCabinet, model.RelationLocatedIn},
		{model.EntityCabinet, model.EntityDevice, model.RelationLocatedIn},
		{model.EntityPod, model.EntityDevice, model.RelationMemberOf},
	} {
		t.Run(string(tc.parent), func(t *testing.T) {
			parent := model.EntityIdentity{SourceID: "s", EntityType: tc.parent, StableID: "parent"}
			child := model.EntityIdentity{SourceID: "s", EntityType: tc.child, StableID: "child"}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{
				readResult(t, []string{"props"}, []*nebula.Value{readMap(readNodeProps(parent))}),
				readResult(t, []string{"node"}, []*nebula.Value{readPeer(t, child)}),
			}}
			page, err := (&topologyReadRepository{client: fake}).Children(context.Background(), parent, TopologyDirectoryQuery{Limit: 1})
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Empty(t, page.NextAfter)
			call := fake.calls[1]
			require.Contains(t, call.statement, "MATCH (n:"+string(tc.child)+")-[e:"+string(model.EdgeSpatial)+"]->(p:"+string(tc.parent)+")")
			require.Contains(t, call.statement, "id(p) == $vid")
			require.Contains(t, call.statement, "properties(e).relation_kind == $relation_kind")
			require.Contains(t, call.statement, "properties(e).source_id == $source_id")
			require.Contains(t, call.statement, "n."+string(tc.child)+".source_id == $source_id")
			require.Contains(t, call.statement, "WITH DISTINCT n AS node, id(n) AS vid ORDER BY vid LIMIT 2")
			require.Equal(t, string(tc.kind), call.params["relation_kind"])
			vid, _ := parent.VID()
			require.Equal(t, vid, call.params["vid"])
			require.Equal(t, "s", call.params["source_id"])
		})
	}
}

func TestTopologyDirectoryArgumentsMissingParentAndEmptyPage(t *testing.T) {
	fake := &topologyGraphFake{}
	repo := &topologyReadRepository{client: fake}
	for _, query := range []TopologyDirectoryQuery{{Limit: 0}, {Limit: 101}, {Limit: 1, After: "signed.token"}, {Limit: 1, After: strings.Repeat("A", 64)}, {Limit: 1, After: strings.Repeat("f", 63)}} {
		_, err := repo.Roots(context.Background(), "s", model.EntityPod, query)
		require.ErrorIs(t, err, ErrInventoryInvalid)
		_, err = repo.Children(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityPod, StableID: "p"}, query)
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	for _, entityType := range []model.EntityType{model.EntityDevice, "bad"} {
		_, err := repo.Roots(context.Background(), "s", entityType, TopologyDirectoryQuery{Limit: 1})
		require.ErrorIs(t, err, ErrInventoryInvalid)
		_, err = repo.Children(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: entityType, StableID: "d"}, TopologyDirectoryQuery{Limit: 1})
		require.ErrorIs(t, err, ErrInventoryInvalid)
	}
	_, err := repo.Roots(context.Background(), "", model.EntityPod, TopologyDirectoryQuery{Limit: 1})
	require.ErrorIs(t, err, ErrInventoryInvalid)
	require.Empty(t, fake.calls)
	fake.results = []*nebulago.ResultSet{readResult(t, []string{"props"})}
	_, err = repo.Children(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityPod, StableID: "missing"}, TopologyDirectoryQuery{Limit: 1})
	require.ErrorIs(t, err, ErrInventoryNotFound)
	require.Len(t, fake.calls, 1)
	fake.results = []*nebulago.ResultSet{readResult(t, []string{"node"})}
	page, err := repo.Roots(context.Background(), "s", model.EntityPod, TopologyDirectoryQuery{Limit: 100})
	require.NoError(t, err)
	require.NotNil(t, page.Items)
	require.Empty(t, page.Items)
	require.Empty(t, page.NextAfter)
}

func TestTopologyDirectoryRejectsCorruptPage(t *testing.T) {
	identity := model.EntityIdentity{SourceID: "s", EntityType: model.EntityPod, StableID: "p"}
	for _, name := range []string{"source", "type", "vid", "tags", "columns", "duplicate", "too many", "after", "unordered"} {
		t.Run(name, func(t *testing.T) {
			value := readPeer(t, identity)
			columns := []string{"node"}
			rows := [][]*nebula.Value{{value}}
			query := TopologyDirectoryQuery{Limit: 1}
			switch name {
			case "source":
				other := identity
				other.SourceID = "other"
				value = readPeer(t, other)
				rows[0][0] = value
			case "type":
				other := identity
				other.EntityType = model.EntityDevice
				rows[0][0] = readPeer(t, other)
			case "vid":
				value.VVal.Vid = readString(strings.Repeat("f", 64))
			case "tags":
				value.VVal.Tags = append(value.VVal.Tags, &nebula.Tag{Name: []byte("device")})
			case "columns":
				columns[0] = "wrong"
			case "duplicate":
				rows = append(rows, rows[0])
			case "too many":
				rows = append(rows, rows[0], rows[0])
			case "after":
				query.After, _ = identity.VID()
			case "unordered":
				other := identity
				other.StableID = "other"
				rows = append(rows, []*nebula.Value{readPeer(t, other)})
				if string(rows[0][0].VVal.Vid.SVal) < string(rows[1][0].VVal.Vid.SVal) {
					rows[0], rows[1] = rows[1], rows[0]
				}
			}
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, columns, rows...)}}
			_, err := (&topologyReadRepository{client: fake}).Roots(context.Background(), "s", model.EntityPod, query)
			require.ErrorIs(t, err, ErrInventoryNotReady)
		})
	}
}
