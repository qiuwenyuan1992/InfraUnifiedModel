package main

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveLinksToSourcePaths(t *testing.T) {
	path := os.Getenv("LINKS_TO_PATH_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对CMDB物理口互联与图四节点路径")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 10)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	source := model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}
	page, err := adapter.NewCMDB(conf).CollectLinksToPage(ctx, source, 1000)
	require.NoError(t, err)
	require.NotEmpty(t, page.Links)
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	reader, ok := graph.(repository.LinksToEndpointReader)
	require.True(t, ok)
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	verified := 0
	for _, candidate := range page.Links {
		if verified == 5 {
			break
		}
		a := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.FromDeviceSN}
		b := model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.ToDeviceSN}
		eligible, err := reader.LinksToEndpointsValid(ctx, a, b, candidate.Identity.From, candidate.Identity.To)
		require.NoError(t, err)
		if !eligible {
			continue
		}
		q := `MATCH (a:device)-[oa:composition_relation]->(pa:interface)-[l:network_relation]->(pb:interface)<-[ob:composition_relation]-(b:device) WHERE a.device.source_id == $source AND b.device.source_id == $source AND a.device.device_sn == $a AND b.device.device_sn == $b AND pa.interface.port_uuid == $pa AND pb.interface.port_uuid == $pb AND oa.source_id == $source AND ob.source_id == $source AND oa.relation_kind == "owns_interface" AND ob.relation_kind == "owns_interface" AND l.source_id == $source AND l.relation_kind == "links_to" RETURN l.relation_id AS id, rank(l) AS rank;`
		r, err := client.ExecuteParameter(ctx, q, map[string]interface{}{"source": source.ID, "a": candidate.FromDeviceSN, "b": candidate.ToDeviceSN, "pa": candidate.Identity.From.StableID, "pb": candidate.Identity.To.StableID})
		require.NoError(t, persistenceCheckGraphResult(r, err))
		require.Equal(t, 1, r.GetRowSize())
		table, err := persistenceTable(r)
		require.NoError(t, err)
		id, err := candidate.Identity.ID()
		require.NoError(t, err)
		rank, err := candidate.Identity.Rank()
		require.NoError(t, err)
		actualRank, err := strconv.ParseInt(table[1][1], 10, 64)
		require.NoError(t, err)
		require.Equal(t, id, persistenceString(table[1][0]))
		require.Equal(t, rank, actualRank)
		t.Logf("verified %s port=%s <-> port=%s %s", candidate.FromDeviceSN, candidate.Identity.From.StableID, candidate.Identity.To.StableID, candidate.ToDeviceSN)
		verified++
	}
	require.Equal(t, 5, verified)
}
