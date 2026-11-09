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

func TestLiveGPURequestTimingReadOnly(t *testing.T) {
	path := os.Getenv("GPU_REQUEST_TIMING_CONFIG")
	if path == "" {
		t.Skip("显式配置后少量只读测量，不执行同步或写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc238"})
	require.NoError(t, err)
	cfg.PageSize = 200
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	servers, err := c.collectUplinkMapPage(ctx, cfg, deviceViewEndpoint, map[string]any{"idc_id": 1028, "parent_type_id": 52, "device_type_id": 58}, 0, []string{"inst_id", "device_sn", "parent_type_id", "device_type_id"})
	require.NoError(t, err)
	t.Logf("GPU服务器总数=%d 首页=%d 请求耗时=%s", *servers.Count, len(*servers.Info), time.Since(start))
	samples := 0
	var total time.Duration
	for i, server := range *servers.Info {
		if i == 3 {
			break
		}
		sn, err := uplinkText(server, "device_sn")
		require.NoError(t, err)
		start = time.Now()
		cards, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/server_gpu", map[string]any{"device_sn": sn}, 0, []string{})
		require.NoError(t, err)
		t.Logf("按服务器SN查卡 server=%s 卡数=%d 耗时=%s", sn, *cards.Count, time.Since(start))
		for _, card := range *cards.Info {
			if samples == 12 {
				break
			}
			gpuSN, err := uplinkText(card, "parts_sn")
			require.NoError(t, err)
			if gpuSN == "" {
				continue
			}
			start = time.Now()
			uplinks, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/gpu_uplink", map[string]any{"gpu_sn": gpuSN}, 0, []string{})
			require.NoError(t, err)
			elapsed := time.Since(start)
			samples++
			total += elapsed
			t.Logf("按GPU SN查上联 gpu=%s 上联数=%d 耗时=%s", gpuSN, *uplinks.Count, elapsed)
		}
	}
	if samples > 0 {
		t.Logf("上联请求样本=%d 总耗时=%s 平均=%s；仅少量样本，不能代表全量耗时", samples, total, total/time.Duration(samples))
	}
}
