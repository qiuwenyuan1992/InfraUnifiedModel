package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLiveOneCabinetPowerReadOnly(t *testing.T) {
	path := os.Getenv("CABINET_POWER_INSPECT_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核查机柜电力引用，不同步或写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc238"})
	require.NoError(t, err)
	cfg.PageSize = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/cabinet", map[string]any{"uuid": "aa4df5a7-1295-a83d-f3df-34504ab3fb9f"}, 0, []string{})
	require.NoError(t, err)
	require.Equal(t, 1, *page.Count)
	row := (*page.Info)[0]
	for _, field := range []string{"inst_id", "uuid", "code", "idc_id", "row_switch_id_A", "row_switch_id_B"} {
		t.Logf("cabinet %s=%s", field, row[field])
	}
	keys := []string{}
	for key := range row {
		if strings.Contains(strings.ToLower(key), "power") || strings.Contains(strings.ToLower(key), "row_switch") || strings.Contains(strings.ToLower(key), "rpp") || strings.Contains(strings.ToLower(key), "pdu") || strings.Contains(strings.ToLower(key), "room") || strings.Contains(strings.ToLower(key), "module") || strings.Contains(strings.ToLower(key), "building") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("power-related %s=%s", key, row[key])
	}
	rpps, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/idc_RPP", map[string]any{"idc": 1030}, 0, []string{})
	require.NoError(t, err)
	t.Logf("IDC1030 RPP总数=%d，本页=%d", *rpps.Count, len(*rpps.Info))
	for i, rpp := range *rpps.Info {
		room, err := uplinkInteger(rpp, "idc_room")
		require.NoError(t, err)
		if room != 3101 {
			continue
		}
		fields := []string{}
		for key := range rpp {
			name := strings.ToLower(key)
			if key == "code" || key == "inst_id" || key == "idc" || strings.Contains(name, "room") || strings.Contains(name, "cabinet") || strings.Contains(name, "module") || strings.Contains(name, "building") || strings.Contains(name, "ups") || strings.Contains(name, "transformer") {
				fields = append(fields, key)
			}
		}
		sort.Strings(fields)
		for _, key := range fields {
			t.Logf("RPP[%d] %s=%s", i, key, rpp[key])
		}
	}
	record, err := cmdbEntityRecord(row, model.EntityCabinet)
	require.NoError(t, err)
	for _, field := range []string{"row_switch_id_a", "row_switch_id_b"} {
		t.Logf("mapped %s=%v type=%T", field, record.Properties[field], record.Properties[field])
	}
	for _, field := range []string{"row_switch_id_A", "row_switch_id_B"} {
		ref, err := uplinkInteger(row, field)
		require.NoError(t, err)
		if ref <= 0 {
			continue
		}
		target, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/idc_RPP", map[string]any{"inst_id": ref}, 0, []string{})
		require.NoError(t, err)
		t.Logf("RPP reference %s=%d matches=%d", field, ref, *target.Count)
		for _, rpp := range *target.Info {
			for _, key := range []string{"inst_id", "code", "name", "idc", "ups_group_id", "transformer_group_A_id"} {
				t.Logf("RPP %s=%s", key, rpp[key])
			}
		}
	}
}
