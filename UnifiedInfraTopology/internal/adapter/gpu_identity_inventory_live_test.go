package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLiveGPUIdentityInventoryReadOnly(t *testing.T) {
	path := os.Getenv("GPU_IDENTITY_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读核对IDC238 GPU卡身份字段，不改变身份模型")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc238"})
	require.NoError(t, err)
	cfg.PageSize = 200
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	servers := []string{}
	for start := 0; ; {
		raw, err := c.collectUplinkMapPage(ctx, cfg, deviceViewEndpoint, map[string]any{"idc_id": 238, "parent_type_id": 52, "device_type_id": 58}, start, []string{"inst_id", "device_sn"})
		require.NoError(t, err)
		for _, row := range *raw.Info {
			sn, err := uplinkText(row, "device_sn")
			require.NoError(t, err)
			servers = append(servers, sn)
		}
		start += len(*raw.Info)
		if start == *raw.Count {
			break
		}
	}
	cards, missing, emptyServers, missingParts := 0, 0, 0, 0
	seen := map[string]GPUCard{}
	duplicates, conflicts := 0, 0
	for _, sn := range servers {
		total := 0
		for start := 0; ; {
			raw, err := c.collectUplinkMapPage(ctx, cfg, "/pub/api/v3/find/instance/object/server_gpu", map[string]any{"device_sn": sn}, start, []string{})
			require.NoError(t, err)
			total = *raw.Count
			for _, row := range *raw.Info {
				cards++
				uuid, err := uplinkText(row, "uuid")
				require.NoError(t, err)
				if uuid == "" {
					missing++
				}
				parts, err := uplinkText(row, "parts_sn")
				require.NoError(t, err)
				if parts == "" {
					missingParts++
					continue
				}
				card, err := cmdbGPUCard(row, "00000000000000000000000000000238", sn)
				require.NoError(t, err)
				if prev, ok := seen[parts]; ok {
					duplicates++
					if !reflect.DeepEqual(prev, card) {
						conflicts++
						t.Logf("duplicate SN=%s first_server=%s other_server=%s first_slot=%v other_slot=%v first_index=%v other_index=%v", parts, prev.ServerSN, card.ServerSN, prev.Properties["slot"], card.Properties["slot"], prev.Properties["card_index"], card.Properties["card_index"])
						for k, v := range card.Properties {
							if !reflect.DeepEqual(v, prev.Properties[k]) {
								t.Logf("SN=%s differing_attribute=%s", parts, k)
							}
						}
					}
				} else {
					seen[parts] = card
				}
			}
			start += len(*raw.Info)
			if start == total {
				break
			}
		}
		if total == 0 {
			emptyServers++
		}
	}
	t.Logf("GPU SN inventory unique_sn=%d duplicate_records=%d conflicting_records=%d", len(seen), duplicates, conflicts)
	t.Logf("GPU identity inventory servers=%d cards=%d missing_uuid=%d missing_parts_sn=%d servers_without_cards=%d", len(servers), cards, missing, missingParts, emptyServers)
}
