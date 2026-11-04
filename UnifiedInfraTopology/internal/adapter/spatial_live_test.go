package adapter

import (
	"context"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveSpatialSourceReadOnly(t *testing.T) {
	path := os.Getenv("SPATIAL_SOURCE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核查空间引用，不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc238.page_size", 200)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	collector := NewCMDB(conf)
	result, err := collector.CollectSpatial(ctx, model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"})
	require.NoError(t, err)
	counts := map[string]int{}
	for _, relation := range result.Relations {
		counts[string(relation.Identity.From.EntityType)+"→"+string(relation.Identity.To.EntityType)]++
	}
	t.Logf("objects=%d edges=%d paths=%v skipped=%d reasons=%v", result.Objects, len(result.Relations), counts, result.Skipped, result.Reasons)
	second, err := collector.CollectSpatial(ctx, model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"})
	require.NoError(t, err)
	require.Equal(t, result.SourceDigest, second.SourceDigest, "独立两遍来源必须一致")
}
