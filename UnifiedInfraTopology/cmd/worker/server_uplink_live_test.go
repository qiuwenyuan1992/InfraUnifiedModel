package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveServerUplinkThreeNodePaths(t *testing.T) {
	path := os.Getenv("SERVER_UPLINK_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对服务器→物理端口←网络设备路径")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 20)
	source := model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := adapter.NewCMDB(conf).CollectServerUplinkPage(ctx, source, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Links)
	config, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(config)
	require.NoError(t, err)
	defer client.Close()
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	reader, ok := graph.(repository.ServerUplinkEndpointReader)
	require.True(t, ok)
	matched, skipped := 0, 0
	for _, link := range page.Links {
		if matched == 5 {
			break
		}
		tor := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: page.Targets[link.To.StableID]}
		eligible, err := reader.ServerUplinkEndpointsValid(ctx, link.From, tor, link.To)
		require.NoError(t, err)
		query := "MATCH (s:device)-[u:network_relation]->(p:interface)<-[o:composition_relation]-(t:device) WHERE s.device.source_id == $source_id AND s.device.device_sn == $server AND p.interface.source_id == $source_id AND p.interface.port_uuid == $port AND t.device.source_id == $source_id AND t.device.device_sn == $tor AND u.source_id == $source_id AND u.relation_kind == $kind AND o.source_id == $source_id AND o.relation_kind == $owner_kind RETURN u.relation_id AS relation_id, rank(u) AS rank, p.interface.port_type AS port_type;"
		result, err := client.ExecuteParameter(ctx, query, map[string]interface{}{"source_id": source.ID, "server": link.From.StableID, "port": link.To.StableID, "tor": page.Targets[link.To.StableID], "kind": "server_uplink", "owner_kind": "owns_interface"})
		require.NoError(t, persistenceCheckGraphResult(result, err))
		if !eligible {
			require.Zero(t, result.GetRowSize(), "被端点保护跳过的来源候选不得有图路径")
			skipped++
			t.Logf("verified skipped server=%s port=%s due to missing/mismatched graph endpoints", link.From.StableID, link.To.StableID)
			continue
		}
		matched++
		require.Equal(t, 1, result.GetRowSize(), "server=%s port=%s", link.From.StableID, link.To.StableID)
		table, err := persistenceTable(result)
		require.NoError(t, err)
		id, err := link.ID()
		require.NoError(t, err)
		rank, err := link.Rank()
		require.NoError(t, err)
		storedRank, err := strconv.ParseInt(table[1][1], 10, 64)
		require.NoError(t, err)
		require.Equal(t, id, persistenceString(table[1][0]))
		require.Equal(t, rank, storedRank)
		require.Equal(t, "physics", persistenceString(table[1][2]))
		t.Logf("verified server=%s -> port=%s <- tor=%s", link.From.StableID, link.To.StableID, page.Targets[link.To.StableID])
	}
	require.Equal(t, 5, matched)
	t.Logf("verified source samples matched=%d intentionally_skipped=%d", matched, skipped)
}
