package service

import (
	"context"
	"os"
	"testing"
	"time"

	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// 只读探针：全图 power_relation 边按 relation_kind 计数，确认电力阶段发布状态。
func TestLivePowerEdgesProbe(t *testing.T) {
	path := os.Getenv("POWER_PROBE_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读探测电力边总数")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	c, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := c.ExecuteParameter(ctx, `MATCH ()-[e:power_relation]->() RETURN properties(e).relation_kind AS kind, count(e) AS total;`, nil)
	require.NoError(t, err)
	require.True(t, r.IsSucceed(), r.GetErrorMsg())
	for _, row := range r.AsStringTable()[1:] {
		t.Logf("power edge kind=%s count=%s", row[0], row[1])
	}
}
