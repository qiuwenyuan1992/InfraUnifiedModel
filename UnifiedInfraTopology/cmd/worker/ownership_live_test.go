package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveDeviceOwnsPhysicalInterfacePaths(t *testing.T) {
	path := os.Getenv("OWNERSHIP_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对设备与物理端口点边路径")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	sn := "210235A53L5264L100G9"
	conf.Set("inventory.cmdb.sources.idc1030.interface_condition", map[string]any{"idc_id": 1030, "sn": sn, "port_type": "physics"})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := adapter.NewCMDB(conf).CollectEntityPage(ctx, model.Source{ConfigRef: "idc1030"}, model.EntityInterface, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	config, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(config)
	require.NoError(t, err)
	defer client.Close()
	source := "00000000000000000000000000001030"
	for index, record := range page.Records {
		if index == 5 {
			break
		}
		identity := model.RelationIdentity{SourceID: source, EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: model.EntityIdentity{SourceID: source, EntityType: model.EntityDevice, StableID: sn}, To: model.EntityIdentity{SourceID: source, EntityType: model.EntityInterface, StableID: record.Identity}}
		id, err := identity.ID()
		require.NoError(t, err)
		rank, err := identity.Rank()
		require.NoError(t, err)
		query := "MATCH (d:device)-[e:composition_relation]->(p:interface) WHERE d.device.source_id == $source_id AND d.device.device_sn == $sn AND p.interface.source_id == $source_id AND p.interface.port_uuid == $port_uuid AND e.source_id == $source_id AND e.relation_kind == $kind RETURN e.relation_id AS relation_id, rank(e) AS rank, p.interface.port_type AS port_type;"
		result, err := client.ExecuteParameter(ctx, query, map[string]interface{}{"source_id": source, "sn": sn, "port_uuid": record.Identity, "kind": "owns_interface"})
		require.NoError(t, persistenceCheckGraphResult(result, err))
		require.Equal(t, 1, result.GetRowSize())
		rows, err := persistenceTable(result)
		require.NoError(t, err)
		require.Equal(t, id, persistenceString(rows[1][0]))
		storedRank, err := strconv.ParseInt(rows[1][1], 10, 64)
		require.NoError(t, err)
		require.Equal(t, rank, storedRank)
		require.Equal(t, "physics", persistenceString(rows[1][2]))
		t.Logf("verified path %s -> owns_interface -> %s", sn, record.Identity)
	}
}
