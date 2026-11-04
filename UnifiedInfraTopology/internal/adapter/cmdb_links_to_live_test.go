package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"encoding/json"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveLinksToSourceReadOnly(t *testing.T) {
	path := os.Getenv("LINKS_TO_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读探测网络设备物理口互联来源，不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	conf.Set("inventory.cmdb.sources.idc1030.page_size", 10)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	start := 0
	if configured := os.Getenv("LINKS_TO_READ_START"); configured != "" {
		value, err := strconv.Atoi(configured)
		require.NoError(t, err)
		start = value
	}
	page, err := NewCMDB(conf).CollectLinksToPage(ctx, model.Source{ID: "00000000000000000000000000001030", ConfigRef: "idc1030"}, start)
	require.NoError(t, err)
	t.Logf("network_devices=%d next=%d candidate_links=%d skipped=%d reasons=%v", page.Total, page.NextOffset, len(page.Links), page.Skipped, page.Reasons)
	if os.Getenv("LINKS_TO_MISSING_SAMPLE") == "1" {
		collector := NewCMDB(conf)
		cfg, err := collector.configFor(model.Source{ConfigRef: "idc1030"})
		require.NoError(t, err)
		require.NotEmpty(t, page.DeviceSNs)
		cfg.PageSize = 200
		ports, err := collector.linksPhysicalRecords(ctx, cfg, page.DeviceSNs[0])
		require.NoError(t, err)
		found := false
		for _, port := range ports {
			sn, err := uplinkText(port, "remote_device_sn")
			require.NoError(t, err)
			uuid, err := uplinkText(port, "remote_interface_uuid")
			require.NoError(t, err)
			if sn == "" || uuid == "" {
				sample := map[string]any{"queried_device_sn": page.DeviceSNs[0], "condition": map[string]string{"sn": page.DeviceSNs[0], "port_type": "physics"}, "raw_port": port}
				encoded, err := json.Marshal(sample)
				require.NoError(t, err)
				t.Logf("missing_remote_sample=%s", encoded)
				found = true
				break
			}
		}
		require.True(t, found, "本页首台设备没有缺失对端样例")
	}
	for index, candidate := range page.Links {
		if index == 5 {
			break
		}
		identity := candidate.Identity
		require.Equal(t, model.RelationLinksTo, identity.Kind)
		require.Less(t, identity.From.StableID, identity.To.StableID)
		t.Logf("network %s port %s <-> port %s network %s", candidate.FromDeviceSN, identity.From.StableID, identity.To.StableID, candidate.ToDeviceSN)
	}
}
