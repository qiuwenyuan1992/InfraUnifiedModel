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

func TestLivePowerSchemaReadOnly(t *testing.T) {
	path := os.Getenv("POWER_SCHEMA_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读查看电力Tag与索引定义")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	client, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, tag := range []string{"transformer", "ups_group", "ups", "rpp"} {
		r, err := client.ExecuteParameter(ctx, "SHOW CREATE TAG "+tag+";", nil)
		require.NoError(t, persistenceCheckGraphResult(r, err))
		t.Logf("schema %s %v", tag, r.AsStringTable())
		r, err = client.ExecuteParameter(ctx, "LOOKUP ON "+tag+" YIELD id(vertex) AS vid | YIELD count(*) AS total;", nil)
		require.NoError(t, persistenceCheckGraphResult(r, err))
		t.Logf("current count %s %v", tag, r.AsStringTable())
	}
	r, err := client.ExecuteParameter(ctx, "SHOW TAG INDEXES;", nil)
	require.NoError(t, persistenceCheckGraphResult(r, err))
	t.Logf("indexes %v", r.AsStringTable())
}
