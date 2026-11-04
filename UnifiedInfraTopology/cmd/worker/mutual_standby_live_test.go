package main

import (
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestLiveMutualStandbyReadOnly(t *testing.T) {
	path := os.Getenv("MUTUAL_STANDBY_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对变压器互备双向关系")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	q := `MATCH (a:transformer)-[e:power_relation]->(b:transformer) WHERE e.source_id == $source AND e.relation_kind == "has_standby" RETURN a.transformer.inst_id AS a_id, b.transformer.inst_id AS b_id, a.transformer.code AS a_code, b.transformer.code AS b_code;`
	result, err := client.ExecuteParameter(ctx, q, map[string]interface{}{"source": "00000000000000000000000000000238"})
	require.NoError(t, persistenceCheckGraphResult(result, err))
	rows, err := persistenceTable(result)
	require.NoError(t, err)
	require.Equal(t, 22, result.GetRowSize())
	pairs := map[string]bool{}
	for _, row := range rows[1:] {
		require.NotEqual(t, row[0], row[1])
		pairs[row[0]+":"+row[1]] = true
		t.Logf("standby %s(%s) -> %s(%s)", persistenceString(row[2]), row[0], persistenceString(row[3]), row[1])
	}
	for _, row := range rows[1:] {
		require.True(t, pairs[row[1]+":"+row[0]], "每条互备必须存在反向边")
	}
	require.True(t, pairs["1047:1109"])
	require.True(t, pairs["1109:1047"])
}
