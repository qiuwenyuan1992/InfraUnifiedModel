package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

func TestTopologyReadSDKValuesAndErrors(t *testing.T) {
	boolean, floating := true, 1.25
	for _, tc := range []struct {
		name string
		raw  *nebula.Value
		want any
	}{
		{"bool", &nebula.Value{BVal: &boolean}, true},
		{"float", &nebula.Value{FVal: &floating}, 1.25},
		{"min", readInt(math.MinInt64), "-9223372036854775808"},
		{"safe", readInt(9007199254740991), int64(9007199254740991)},
		{"unsafe", readInt(9007199254740992), "9007199254740992"},
		{"date", &nebula.Value{DVal: &nebula.Date{Year: 2026, Month: 11, Day: 5}}, "2026-11-05"},
		{"empty list", &nebula.Value{LVal: &nebula.NList{}}, []any{}},
		{"set", &nebula.Value{UVal: &nebula.NSet{Values: []*nebula.Value{readString("b"), readString("a")}}}, []any{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := topologyReadRow(readResult(t, []string{"value"}, []*nebula.Value{tc.raw}), 0)
			require.NoError(t, err)
			got, err := topologyReadValue(values[0])
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			_, err = json.Marshal(got)
			require.NoError(t, err)
		})
	}
	for _, null := range []nebula.NullType{nebula.NullType_NaN, nebula.NullType_BAD_DATA, nebula.NullType_BAD_TYPE, nebula.NullType_ERR_OVERFLOW, nebula.NullType_UNKNOWN_PROP, nebula.NullType_DIV_BY_ZERO} {
		values, err := topologyReadRow(readResult(t, []string{"value"}, []*nebula.Value{{LVal: &nebula.NList{Values: []*nebula.Value{{NVal: &null}}}}}), 0)
		require.NoError(t, err)
		_, err = topologyReadValue(values[0])
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
	for _, floating := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		values, err := topologyReadRow(readResult(t, []string{"value"}, []*nebula.Value{{FVal: &floating}}), 0)
		require.NoError(t, err)
		_, err = topologyReadValue(values[0])
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
}

type readClosableClient struct {
	topologyGraphFake
	closed bool
}

func (client *readClosableClient) Close() { client.closed = true }

func TestTopologyReadConstructorOwnsSingleClientAndCleanup(t *testing.T) {
	conf := viper.New()
	conf.Set("inventory.graph.hosts", []string{"localhost:9669"})
	conf.Set("inventory.graph.space", "topology")
	conf.Set("inventory.graph.username", "user")
	conf.Set("inventory.graph.password", "secret")
	client := &readClosableClient{}
	opened := 0
	repo, cleanup, err := newTopologyReadRepository(conf, func(config graphclient.Config) (graphclient.Client, error) {
		opened++
		require.Equal(t, "topology", config.Space)
		return client, nil
	})
	require.NoError(t, err)
	_, err = repo.Node(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"})
	require.ErrorIs(t, err, ErrInventoryNotReady)
	require.Equal(t, 1, opened)
	cleanup()
	require.True(t, client.closed)
	repo, cleanup, err = newTopologyReadRepository(conf, func(graphclient.Config) (graphclient.Client, error) { return nil, errors.New("connection failed") })
	require.NoError(t, err, "图库故障不应阻止HTTP管理服务启动")
	cleanup()
	_, err = repo.Node(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"})
	require.ErrorIs(t, err, ErrInventoryNotReady)
	conf.Set("inventory.graph.space", "invalid-space")
	repo, cleanup, err = newTopologyReadRepository(conf, func(graphclient.Config) (graphclient.Client, error) {
		t.Fatal("invalid config opened a client")
		return nil, nil
	})
	require.NoError(t, err)
	cleanup()
	_, err = repo.Node(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"})
	require.ErrorIs(t, err, ErrInventoryNotReady)
}

func TestTopologyReadCancellationAndRejectedResult(t *testing.T) {
	id := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &topologyGraphFake{}
	_, err := (&topologyReadRepository{client: fake}).Node(ctx, id)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, fake.calls)
	ctx, cancel = context.WithCancel(context.Background())
	fake.onExecute = func(context.Context) { cancel() }
	_, err = (&topologyReadRepository{client: fake}).Node(ctx, id)
	require.ErrorIs(t, err, context.Canceled)
	fake.onExecute = nil
	fake.results = []*nebulago.ResultSet{topologyGraphRejectedResult(t, "secret source")}
	_, err = (&topologyReadRepository{client: fake}).Node(context.Background(), id)
	require.ErrorIs(t, err, ErrInventoryNotReady)
	require.NotContains(t, err.Error(), "secret")
}

func TestTopologyReadDeclaredJSONListsOnly(t *testing.T) {
	for _, tc := range []struct {
		entity model.EntityType
		field  string
		stored string
		want   []any
	}{
		{model.EntityDevice, "all_ips", `["10.0.0.1","10.0.0.2"]`, []any{"10.0.0.1", "10.0.0.2"}},
		{model.EntityGPU, "conn_ports", `["p1","p2"]`, []any{"p1", "p2"}},
		{model.EntityCabinet, "pod_ids", `[42,9223372036854775807]`, []any{int64(42), "9223372036854775807"}},
	} {
		t.Run(string(tc.entity), func(t *testing.T) {
			id := model.EntityIdentity{SourceID: "s", EntityType: tc.entity, StableID: "id"}
			props := readNodeProps(id)
			props[tc.field] = readString(tc.stored)
			props["name"] = readString(`["not","a","list"]`)
			fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(props)})}}
			node, err := (&topologyReadRepository{client: fake}).Node(context.Background(), id)
			require.NoError(t, err)
			require.Equal(t, tc.want, node.Properties[tc.field])
			require.Equal(t, `["not","a","list"]`, node.Properties["name"])
			values, err := topologyReadRow(readResult(t, []string{"node"}, []*nebula.Value{{VVal: &nebula.Vertex{Vid: readString(node.ID), Tags: []*nebula.Tag{{Name: []byte(tc.entity), Props: props}}}}}), 0)
			require.NoError(t, err)
			peer, err := topologyReadSDKNode(values[0])
			require.NoError(t, err)
			require.Equal(t, tc.want, peer.Properties[tc.field])
		})
	}
	for _, stored := range []string{`[1]`, `{}`, `["a"] trailing`, `null`, `invalid`} {
		id := model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "d"}
		props := readNodeProps(id)
		props["all_ips"] = readString(stored)
		fake := &topologyGraphFake{results: []*nebulago.ResultSet{readResult(t, []string{"props"}, []*nebula.Value{readMap(props)})}}
		_, err := (&topologyReadRepository{client: fake}).Node(context.Background(), id)
		require.ErrorIs(t, err, ErrInventoryNotReady)
	}
}
