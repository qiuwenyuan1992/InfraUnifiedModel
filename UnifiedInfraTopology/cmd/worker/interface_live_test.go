package main

import (
	"context"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveRetainedPhysicalPortsMatchCMDB(t *testing.T) {
	path := os.Getenv("PHYSICAL_PORT_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式提供配置后只读对比来源物理端口和已保留图节点")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 200)
	conf.Set("inventory.cmdb.sources.idc1030.interface_condition", map[string]any{"idc_id": 1030, "sn": "210235A53L5264L100G9", "port_type": "physics"})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := adapter.NewCMDB(conf).CollectEntityPage(ctx, model.Source{ConfigRef: "idc1030"}, model.EntityInterface, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	graphConfig, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(graphConfig)
	require.NoError(t, err)
	defer client.Close()
	fields := []string{"port_name", "port_type", "port_speed", "port_operation_status", "group_uuid", "port_group_uuid", "group_name"}
	for index, record := range page.Records {
		if index == 5 {
			break
		}
		vid, err := (model.EntityIdentity{SourceID: "00000000000000000000000000001030", EntityType: model.EntityInterface, StableID: record.Identity}).VID()
		require.NoError(t, err)
		statement := "FETCH PROP ON interface \"" + vid + "\" YIELD properties(vertex).port_uuid AS port_uuid"
		for _, field := range fields {
			statement += ", properties(vertex)." + field + " AS " + field
		}
		result, err := client.ExecuteParameter(ctx, statement+";", nil)
		require.NoError(t, persistenceCheckGraphResult(result, err))
		require.Equal(t, 1, result.GetRowSize())
		table, err := persistenceTable(result)
		require.NoError(t, err)
		row := table[1]
		require.Equal(t, record.Identity, persistenceString(row[0]))
		for fieldIndex, field := range fields {
			value, err := persistenceNullableText(row[fieldIndex+1])
			require.NoError(t, err, "field=%s encoded=%q", field, row[fieldIndex+1])
			if record.Properties[field] == nil {
				require.Nil(t, value, field)
			} else {
				require.NotNil(t, value, field)
				require.Equal(t, record.Properties[field], *value, field)
			}
		}
		t.Logf("matched port_uuid=%s name=%v speed=%v status=%v type=%v", record.Identity, record.Properties["port_name"], record.Properties["port_speed"], record.Properties["port_operation_status"], record.Properties["port_type"])
	}
}
