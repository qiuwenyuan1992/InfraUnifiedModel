package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestIDC238PowerSourceReadOnly(t *testing.T) {
	path := os.Getenv("IDC238_POWER_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读诊断IDC238电力来源")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	s := model.Source{ConfigRef: "idc238"}
	cfg, err := c.configFor(s)
	require.NoError(t, err)
	for _, spec := range []struct {
		entity model.EntityType
		object string
		fields []string
	}{{model.EntityTransformer, "idc_transformer", transformerFields}, {model.EntityUPSGroup, "idc_ups_group", upsGroupFields}, {model.EntityUPS, "idc_ups", upsFields}, {model.EntityRPP, "idc_RPP", rppFields}} {
		raw, err := c.collectUplinkMapPage(context.Background(), cfg, "/pub/api/v3/find/instance/object/"+spec.object, map[string]any{"idc": 238}, 0, spec.fields)
		require.NoError(t, err)
		require.Equal(t, *raw.Count, len(*raw.Info))
		missing, invalid := 0, 0
		for _, item := range *raw.Info {
			uuid, err := uplinkText(item, "uuid")
			require.NoError(t, err)
			if uuid == "" {
				missing++
			}
			if _, err := cmdbEntityRecord(item, spec.entity); err != nil {
				invalid++
			}
		}
		t.Logf("entity=%s total=%d missing_uuid=%d invalid_records=%d", spec.entity, *raw.Count, missing, invalid)
	}
	raw, err := c.collectUplinkMapPage(context.Background(), cfg, "/pub/api/v3/find/instance/object/cabinet", map[string]any{"idc_id": 238}, 0, []string{"inst_id", "uuid", "code", "row_switch_id_A", "row_switch_id_B"})
	require.NoError(t, err)
	require.Equal(t, *raw.Count, len(*raw.Info))
	zero, positive, invalid := 0, 0, 0
	for _, item := range *raw.Info {
		for _, field := range []string{"row_switch_id_A", "row_switch_id_B"} {
			value, err := uplinkInteger(item, field)
			if err != nil {
				invalid++
				if invalid <= 5 {
					t.Logf("cabinet=%s field=%s value=%s", item["code"], field, item[field])
				}
				continue
			}
			if value == 0 {
				zero++
			} else {
				positive++
			}
		}
	}
	t.Logf("IDC238 cabinets=%d zero_power_refs=%d nonzero_power_refs=%d invalid_or_missing_refs=%d", *raw.Count, zero, positive, invalid)
}
