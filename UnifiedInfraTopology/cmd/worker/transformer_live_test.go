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

func TestLiveRetainedTransformersMatchCMDB(t *testing.T) {
	path := os.Getenv("TRANSFORMER_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读对比来源变压器和已存图节点")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.transformer_condition", map[string]any{"idc": 1030})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := adapter.NewCMDB(conf).CollectEntityPage(ctx, model.Source{ConfigRef: "idc1030"}, model.EntityTransformer, 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)
	config, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(config)
	require.NoError(t, err)
	defer client.Close()
	fields := []string{"code", "brand", "model", "inst_id", "idc_id", "standby_transformer_id", "rated_capacity"}
	for index, record := range page.Records {
		if index == 5 {
			break
		}
		vid, err := (model.EntityIdentity{SourceID: "00000000000000000000000000001030", EntityType: model.EntityTransformer, StableID: record.Identity}).VID()
		require.NoError(t, err)
		statement := "FETCH PROP ON transformer \"" + vid + "\" YIELD properties(vertex).uuid AS uuid"
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
			encoded := row[fieldIndex+1]
			if record.Properties[field] == nil {
				require.Equal(t, "null", encoded, field)
				continue
			}
			if fieldIndex < 3 {
				require.Equal(t, record.Properties[field], persistenceString(encoded), field)
			} else {
				value, err := strconv.ParseInt(encoded, 10, 64)
				require.NoError(t, err, field)
				require.Equal(t, record.Properties[field], value, field)
			}
		}
		t.Log(fmt.Sprintf("matched uuid=%s code=%v brand=%v model=%v capacity=%v", record.Identity, record.Properties["code"], record.Properties["brand"], record.Properties["model"], record.Properties["rated_capacity"]))
	}
}
