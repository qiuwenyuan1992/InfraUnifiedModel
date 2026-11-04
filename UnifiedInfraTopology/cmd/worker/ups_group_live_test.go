package main

import (
	"context"
	"fmt"
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

func TestLiveRetainedUPSGroupsMatchCMDB(t *testing.T) {
	path := os.Getenv("UPS_GROUP_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读对比来源 UPS 组与已存节点，不写数据库")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.ups_group_condition", map[string]any{"idc": 1030})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := adapter.NewCMDB(conf).CollectEntityPage(ctx, model.Source{ConfigRef: "idc1030"}, model.EntityUPSGroup, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	fields := []string{"code", "inst_id", "idc_id", "transformer_id_up"}
	for index, record := range page.Records {
		if index == 5 {
			break
		}
		instID, err := strconv.ParseInt(record.Identity, 10, 64)
		require.NoError(t, err)
		identity := strconv.FormatInt(instID, 10)
		vid, err := (model.EntityIdentity{SourceID: "00000000000000000000000000001030", EntityType: model.EntityUPSGroup, StableID: identity}).VID()
		require.NoError(t, err)
		query := fmt.Sprintf("FETCH PROP ON ups_group %q YIELD properties(vertex).inst_id AS identity", vid)
		for _, field := range fields {
			query += ", properties(vertex)." + field + " AS " + field
		}
		result, err := client.ExecuteParameter(ctx, query+";", nil)
		require.NoError(t, persistenceCheckGraphResult(result, err))
		require.Equal(t, 1, result.GetRowSize())
		table, err := persistenceTable(result)
		require.NoError(t, err)
		row := table[1]
		require.Equal(t, identity, persistenceString(row[0]))
		for fieldIndex, field := range fields {
			if record.Properties[field] == nil {
				require.Equal(t, "null", row[fieldIndex+1], field)
				continue
			}
			if field == "code" {
				require.Equal(t, record.Properties[field], persistenceString(row[fieldIndex+1]), field)
			} else {
				value, err := strconv.ParseInt(row[fieldIndex+1], 10, 64)
				require.NoError(t, err, field)
				require.Equal(t, record.Properties[field], value, field)
			}
		}
		t.Logf("matched inst_id=%s code=%v idc=%v transformer_id_up=%v", identity, record.Properties["code"], record.Properties["idc_id"], record.Properties["transformer_id_up"])
	}
}
