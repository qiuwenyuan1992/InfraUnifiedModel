package main

import (
	"context"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/cmd/server/wire"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLiveTopologyBrowserServer(t *testing.T) {
	path := os.Getenv("TOPOLOGY_BROWSER_SERVER_CONFIG")
	if path == "" {
		t.Skip("显式配置后启动临时本地HTTP供浏览器只读验收；默认不访问外部服务")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("http.host", "127.0.0.1")
	conf.Set("http.port", 18000)
	app, cleanup, err := wire.NewWire(conf, &log.Logger{Logger: zap.NewNop()})
	require.NoError(t, err)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	require.NoError(t, app.Run(ctx))
}
