package service

import (
	"UnifiedInfraTopology/internal/adapter"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestLiveLinksToCollectionAndEndpointsReadOnly(t *testing.T) {
	path := os.Getenv("LINKS_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读采集和检查物理端点，不写关系")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	graph, cleanup, err := repository.NewTopologyGraphRepository(conf)
	require.NoError(t, err)
	defer cleanup()
	worker := NewLinksToWorker(adapter.NewCMDB(conf), graph)
	source := model.Source{ID: "00000000000000000000000000000238", ConfigRef: "idc238"}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	first, candidates, count, skipped, reasons, err := worker.collect(ctx, source)
	require.NoError(t, err)
	t.Logf("devices=%d candidates=%d skipped=%d reasons=%v", count, len(candidates), skipped, reasons)
	reader := graph.(repository.LinksToEndpointReader)
	missing := 0
	for _, candidate := range candidates {
		valid, e := reader.LinksToEndpointsValid(ctx, model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.FromDeviceSN}, model.EntityIdentity{SourceID: source.ID, EntityType: model.EntityDevice, StableID: candidate.ToDeviceSN}, candidate.Identity.From, candidate.Identity.To)
		require.NoError(t, e)
		if !valid {
			missing++
		}
	}
	second, _, _, _, _, err := worker.collect(ctx, source)
	require.NoError(t, err)
	t.Logf("missing_endpoints=%d source_digest_equal=%v", missing, first == second)
	require.Equal(t, first, second)
}
