package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveGPUOneServerReadOnly(t *testing.T) {
	path := os.Getenv("GPU_SERVER_INSPECT_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核查单服务器卡与上联，不同步或写图")
	}
	sn := os.Getenv("GPU_SERVER_INSPECT_SN")
	require.NotEmpty(t, sn)
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc238"})
	require.NoError(t, err)
	cfg.PageSize = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cards, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/server_gpu", map[string]any{"device_sn": sn}, 0, []string{})
	require.NoError(t, err)
	links, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/gpu_uplink", map[string]any{"device_sn": sn}, 0, []string{})
	require.NoError(t, err)
	require.Equal(t, *cards.Count, len(*cards.Info))
	require.Equal(t, *links.Count, len(*links.Info))
	mapped := map[string]GPUCard{}
	for _, row := range *cards.Info {
		card, err := cmdbGPUCard(row, "00000000000000000000000000000238", sn)
		require.NoError(t, err)
		mapped[card.Identity.StableID] = card
		t.Logf("card=%s slot=%v conn_ports=%v", card.Identity.StableID, card.Properties["slot"], card.Properties["conn_ports"])
	}
	t.Logf("CMDB server=%s cards=%d uplinks=%d", sn, *cards.Count, *links.Count)
	torCache := map[string]map[string]string{}
	for _, row := range *links.Info {
		fields := map[string]string{}
		for _, field := range []string{"uuid", "device_sn", "gpu_sn", "gpu_port", "gpu_slot", "tor_sn", "tor_port"} {
			value, err := uplinkText(row, field)
			require.NoError(t, err)
			fields[field] = value
		}
		rawToRPort := fields["tor_port"]
		fields["gpu_port"] = NormalizePortName(fields["gpu_port"])
		fields["tor_port"] = NormalizePortName(fields["tor_port"])
		card, found := mapped[fields["gpu_sn"]]
		portMatch := fields["gpu_port"] == ""
		if found {
			for _, port := range card.Properties["conn_ports"].([]string) {
				if port == fields["gpu_port"] {
					portMatch = true
				}
			}
		}
		slotMatch := !found || fields["gpu_slot"] == "" || card.Properties["slot"] == nil || card.Properties["slot"] == "" || card.Properties["slot"] == fields["gpu_slot"]
		tor := fields["tor_sn"]
		if _, ok := torCache[tor]; !ok && tor != "" {
			ports, err := c.collectUplinkToRPorts(ctx, cfg, tor)
			require.NoError(t, err)
			torCache[tor] = ports
			devices, err := c.collectUplinkMapPage(ctx, cfg, deviceViewEndpoint, map[string]any{"device_sn": tor, "parent_type_id": 54}, 0, []string{"inst_id", "device_sn", "parent_type_id"})
			require.NoError(t, err)
			rawPorts, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/port_view", map[string]any{"sn": tor}, 0, []string{"inst_id", "port_uuid", "port_name", "local_device_sn"})
			require.NoError(t, err)
			matching := 0
			for _, p := range *rawPorts.Info {
				name, err := uplinkText(p, "port_name")
				require.NoError(t, err)
				if name == fields["tor_port"] || strings.Contains(name, "0/35") {
					matching++
					t.Logf("ToR=%s candidate_port=%s uuid=%s inst_id=%s", tor, name, p["port_uuid"], p["inst_id"])
				}
			}
			t.Logf("ToR=%s network_device_matches=%d total_ports=%d candidate_matches=%d", tor, *devices.Count, *rawPorts.Count, matching)
		}
		t.Logf("uplink=%v raw_tor_port=%q card_found=%v gpu_port_match=%v slot_match=%v tor_port_uuid=%s", fields, rawToRPort, found, portMatch, slotMatch, torCache[tor][fields["tor_port"]])
		if os.Getenv("GPU_SERVER_REQUIRE_MATCH") == "1" {
			require.True(t, found)
			require.True(t, portMatch)
			require.True(t, slotMatch)
			require.NotEmpty(t, torCache[tor][fields["tor_port"]], "规范化后必须匹配真实端口")
		}
	}
}
