package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestLiveUnresolvedUplinkPortNamesReadOnly(t *testing.T) {
	path := os.Getenv("UPLINK_MISSING_PORT_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后按两台ToR SN只读核对未解析端口，包含虚拟口但不写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc1030"})
	require.NoError(t, err)
	cfg.PageSize = 200
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	targets := map[string][]string{"3G2610019347": {"GE0/0/5", "GE0/0/6"}, "102618636126": {"25GE1/0/5", "25GE1/0/6", "25GE1/0/7", "25GE1/0/8"}}
	for sn, names := range targets {
		records := []map[string]string{}
		total := -1
		for start := 0; ; {
			raw, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/port_view", map[string]any{"sn": sn}, start, []string{"inst_id", "port_uuid", "port_name", "port_type", "local_device_sn"})
			require.NoError(t, err)
			if total < 0 {
				total = *raw.Count
			}
			require.Equal(t, total, *raw.Count)
			for _, item := range *raw.Info {
				record := map[string]string{}
				for _, field := range []string{"port_uuid", "port_name", "port_type"} {
					value, err := uplinkText(item, field)
					require.NoError(t, err)
					record[field] = value
				}
				records = append(records, record)
			}
			start += len(*raw.Info)
			if start == total {
				break
			}
		}
		for _, name := range names {
			matches := []map[string]string{}
			for _, record := range records {
				if record["port_name"] == name {
					matches = append(matches, record)
				}
			}
			t.Logf("tor_sn=%s required_name=%s exact_matches=%v all_port_count=%d", sn, name, matches, total)
		}
		t.Logf("tor_sn=%s all_port_names=%v", sn, records)
	}
}
