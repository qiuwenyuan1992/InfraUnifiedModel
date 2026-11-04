package adapter

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestLiveCabinetPowerReferencesReadOnly(t *testing.T) {
	path := os.Getenv("POWER_REFS_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读抽查机柜A/B来源值")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	c := NewCMDB(conf)
	cfg, err := c.configFor(model.Source{ConfigRef: "idc1030"})
	require.NoError(t, err)
	cfg.PageSize = 20000
	page, err := c.collectUplinkMapPage(context.Background(), cfg, "/pub/api/v3/find/instance/object/cabinet", map[string]any{"idc_id": 1030}, 0, []string{"inst_id", "uuid", "code", "row_switch_id_A", "row_switch_id_B"})
	require.NoError(t, err)
	require.Equal(t, *page.Count, len(*page.Info))
	zero, positive := 0, 0
	for index, row := range *page.Info {
		if index < 5 {
			t.Logf("code=%s A=%s B=%s", row["code"], row["row_switch_id_A"], row["row_switch_id_B"])
		}
		for _, field := range []string{"row_switch_id_A", "row_switch_id_B"} {
			value, err := uplinkInteger(row, field)
			require.NoError(t, err)
			if value == 0 {
				zero++
			} else {
				positive++
			}
		}
	}
	t.Logf("cabinet_count=%d zero_refs=%d nonzero_refs=%d", len(*page.Info), zero, positive)
}
