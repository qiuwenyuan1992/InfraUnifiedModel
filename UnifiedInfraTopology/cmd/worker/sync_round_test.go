package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/service"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestSyncRoundPhaseOrderAndFailureBoundary(t *testing.T) {
	want := []string{"data_center", "room", "cabinet", "device", "transformer", "ups_group", "ups", "rpp", "pod", "interface", "power_relations", "pod_membership", "spatial_relations", "gpu_sync", "server_uplinks", "links_to"}
	require.Equal(t, want, syncRoundPhaseNames())
	for _, failed := range []int{-1, 0, 11, len(want) - 1} {
		t.Run(want[max(0, failed)], func(t *testing.T) {
			conf := viper.New()
			conf.Set("inventory.cmdb.sources.test.device_condition.idc_id", 238)
			at := time.Date(2026, 11, 3, 0, 0, 0, 123456789, time.FixedZone("offset", 3600))
			calls := []string{}
			stages := []syncRoundStage{}
			for i, name := range want {
				idx, phase := i, name
				stages = append(stages, syncRoundStage{Name: phase, Run: func(ctx context.Context, c *viper.Viper, out *bytes.Buffer) error {
					calls = append(calls, phase)
					require.Equal(t, at.UTC().Truncate(time.Microsecond), service.SyncSnapshot(ctx, time.Now()))
					require.Equal(t, 238, c.GetInt("inventory.cmdb.sources.test.device_condition.idc_id"))
					require.Empty(t, c.GetString("inventory.cmdb.sources.test.entity_type"))
					c.Set("inventory.cmdb.sources.test.entity_type", phase)
					c.Set("inventory.cmdb.sources.test.device_condition.idc_id", 999)
					_ = json.NewEncoder(out).Encode(map[string]string{"status": "succeeded", "run_id": phase})
					if idx == failed {
						return errors.New("secret must not reach round report")
					}
					return nil
				}})
			}
			var out bytes.Buffer
			err := executeSyncRound(context.Background(), conf, "00000000000000000000000000000238", at, stages, &out)
			count := len(want)
			if failed >= 0 {
				count = failed + 1
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, want[:count], calls)
			require.Equal(t, 238, conf.GetInt("inventory.cmdb.sources.test.device_condition.idc_id"))
			require.Empty(t, conf.GetString("inventory.cmdb.sources.test.entity_type"))
			decoder := json.NewDecoder(&out)
			var report syncRoundReport
			for decoder.More() {
				var event json.RawMessage
				require.NoError(t, decoder.Decode(&event))
				var candidate syncRoundReport
				require.NoError(t, json.Unmarshal(event, &candidate))
				if len(candidate.Phases) > 0 {
					report = candidate
				}
			}
			require.Len(t, report.Phases, len(want))
			require.NotContains(t, out.String(), "secret")
			for i, phase := range report.Phases {
				status := "succeeded"
				if failed >= 0 && i == failed {
					status = "failed"
				}
				if failed >= 0 && i > failed {
					status = "not_run"
				}
				require.Equal(t, status, phase.Status)
			}
		})
	}
}

func TestSyncRoundRejectsInvalidSelectionBeforeStages(t *testing.T) {
	var out bytes.Buffer
	for _, source := range []string{"", "invalid"} {
		require.Error(t, executeSyncRound(context.Background(), viper.New(), source, time.Now(), nil, &out))
	}
	require.Error(t, executeSyncRound(context.Background(), viper.New(), "00000000000000000000000000000238", time.Time{}, nil, &out))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, executeSyncRound(ctx, viper.New(), "00000000000000000000000000000238", time.Now(), nil, &out))
}

func TestSyncRoundSelectionsRequireCompatibleRoomIDC(t *testing.T) {
	conf := viper.New()
	prefix := "inventory.cmdb.sources.test."
	for _, key := range []string{"device_condition.idc_id", "room_condition.idc_id", "cabinet_condition.idc_id", "data_center_condition.inst_id", "interface_condition.idc_id", "transformer_condition.idc", "ups_group_condition.idc", "ups_condition.idc", "rpp_condition.idc"} {
		conf.Set(prefix+key, 238)
	}
	require.NoError(t, validateSyncRoundSelections(conf, "test"))
	for _, idc := range []int{0, 1028} {
		conf.Set(prefix+"room_condition.idc_id", idc)
		require.Error(t, validateSyncRoundSelections(conf, "test"))
	}
}

func TestRoundAndSpatialModesAreExclusive(t *testing.T) {
	require.True(t, validRoundSpatialModes(true, false, false, nil, "source", "", ""))
	require.True(t, validRoundSpatialModes(false, false, true, nil, "source", "", "run"))
	require.False(t, validRoundSpatialModes(true, true, false, nil, "source", "", ""))
	require.False(t, validRoundSpatialModes(true, false, false, []bool{true}, "source", "", ""))
	require.False(t, validRoundSpatialModes(true, false, false, nil, "source", "device", ""))
	require.False(t, validRoundSpatialModes(false, true, false, nil, "source", "", "run"))
}

func TestSyncRoundRetainsFailedRunWithoutLeakingError(t *testing.T) {
	var out bytes.Buffer
	stages := []syncRoundStage{{Name: "spatial_relations", Run: func(context.Context, *viper.Viper, *bytes.Buffer) error {
		return errors.New("spatial_relations run abcdefabcdefabcdefabcdefabcdefab failed; credential must not leak")
	}}}
	require.Error(t, executeSyncRound(context.Background(), viper.New(), "00000000000000000000000000000238", time.Now(), stages, &out))
	require.Contains(t, out.String(), "abcdefabcdefabcdefabcdefabcdefab")
	require.NotContains(t, out.String(), "credential")
}
