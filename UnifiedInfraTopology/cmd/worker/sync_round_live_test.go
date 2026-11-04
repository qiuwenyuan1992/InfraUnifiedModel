package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/service"
	"UnifiedInfraTopology/pkg/log"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLiveApprovedSyncRound(t *testing.T) {
	path := os.Getenv("SYNC_ROUND_CONFIG")
	if path == "" {
		t.Skip("完整同步写图必须显式配置及授权；默认不访问外部服务")
	}
	require.Equal(t, "IDC238_RETAIN_GRAPH", os.Getenv("SYNC_ROUND_CONFIRM"))
	reportPath := os.Getenv("SYNC_ROUND_REPORT")
	require.NotEmpty(t, reportPath)
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	logger := log.NewLog(conf)
	defer logger.Sync()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	var output bytes.Buffer
	err := runSyncRound(ctx, conf, logger, "00000000000000000000000000000238", &output)
	decoder := json.NewDecoder(&output)
	var report syncRoundReport
	for {
		var raw json.RawMessage
		e := decoder.Decode(&raw)
		if e == io.EOF {
			break
		}
		require.NoError(t, e)
		var candidate syncRoundReport
		require.NoError(t, json.Unmarshal(raw, &candidate))
		if len(candidate.Phases) > 0 {
			report = candidate
		}
	}
	if len(report.Phases) > 0 {
		data, e := json.MarshalIndent(report, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(reportPath, append(data, 10), 0600))
		t.Logf("round status=%s snapshot=%s report=%s", report.Status, report.SnapshotAt.Format(time.RFC3339Nano), reportPath)
		for _, phase := range report.Phases {
			t.Logf("phase=%s status=%s run_id=%s", phase.Name, phase.Status, phase.RunID)
		}
	}
	require.NoError(t, err)
	require.Equal(t, "PASS", report.Status)
	require.Len(t, report.Phases, len(syncRoundPhaseNames()))
	for _, phase := range report.Phases {
		require.Equal(t, "succeeded", phase.Status)
		require.Regexp(t, inventoryID, phase.RunID)
	}
}

func TestLiveRecordedSyncRoundVerification(t *testing.T) {
	path := os.Getenv("SYNC_ROUND_VERIFY_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读独立复核已保存的完整轮次")
	}
	content, err := os.ReadFile(os.Getenv("SYNC_ROUND_VERIFY_REPORT"))
	require.NoError(t, err)
	var report syncRoundReport
	require.NoError(t, json.Unmarshal(content, &report))
	require.Equal(t, "PASS", report.Status)
	require.True(t, inventoryID.MatchString(report.SourceID))
	require.Len(t, report.Phases, len(syncRoundPhaseNames()))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	ctx, cancel := context.WithTimeout(service.WithSyncSnapshot(context.Background(), report.SnapshotAt), 10*time.Minute)
	defer cancel()
	for i, phase := range report.Phases {
		require.Equal(t, syncRoundPhaseNames()[i], phase.Name)
		require.Equal(t, "succeeded", phase.Status)
		require.True(t, inventoryID.MatchString(phase.RunID))
		config, err := cloneRoundConfig(conf)
		require.NoError(t, err)
		var output bytes.Buffer
		switch phase.Name {
		case "power_relations":
			err = verifyPowerSync(ctx, config, report.SourceID, phase.RunID, &output)
		case "pod_membership":
			err = verifyPODMembership(ctx, config, report.SourceID, phase.RunID, &output)
		case "spatial_relations":
			err = verifySpatialSync(ctx, config, report.SourceID, phase.RunID, &output)
		case "gpu_sync":
			err = verifyGPUSync(ctx, config, report.SourceID, phase.RunID, &output)
		case "server_uplinks":
			err = verifyServerUplink(ctx, config, report.SourceID, phase.RunID, &output)
		case "links_to":
			err = verifyLinksTo(ctx, config, report.SourceID, phase.RunID, &output)
		default:
			err = verifyPersistence(ctx, config, report.SourceID, model.EntityType(phase.Name), phase.RunID, &output)
		}
		require.NoError(t, err, "phase=%s run=%s", phase.Name, phase.RunID)
		t.Logf("independent verification PASS phase=%s run_id=%s shared_snapshot=%s", phase.Name, phase.RunID, report.SnapshotAt.Format(time.RFC3339Nano))
	}
}
