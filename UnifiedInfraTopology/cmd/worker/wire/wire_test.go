package wire

import (
	"context"
	"testing"

	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkerWiringBuildsWithoutExternalGraph(t *testing.T) {
	conf := viper.New()
	conf.Set("data.db.main.driver", "sqlite")
	conf.Set("data.db.main.dsn", "file::memory:?cache=shared")
	conf.Set("inventory.graph.hosts", []string{})
	conf.Set("inventory.graph.space", "")

	worker, cleanup, err := NewWire(conf, &log.Logger{Logger: zap.NewNop()})
	require.NoError(t, err)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, worker.Run(ctx))
}
