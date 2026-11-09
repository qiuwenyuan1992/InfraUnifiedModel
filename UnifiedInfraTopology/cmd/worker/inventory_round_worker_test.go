package main

import (
	"UnifiedInfraTopology/internal/service"
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRoundStageResultRequiresMatchingVerifiedCheckpoint(t *testing.T) {
	at := time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)
	good := `{"status":"succeeded","run_id":"abcdefabcdefabcdefabcdefabcdefab","result":{"nodes":2,"edges":3,"objects":5,"skipped":1,"reasons":{"gpu_missing_parts_sn":1}}}
 {"status":"PASS","source_id":"00000000000000000000000000000238","run":{"id":"abcdefabcdefabcdefabcdefabcdefab","status":"succeeded"},"checkpoint":{"complete":true,"cursor":{"run_id":"abcdefabcdefabcdefabcdefabcdefab","snapshot_at":"2026-11-03T01:02:03Z","total":2}}}`
	result, err := roundStageResult(bytes.NewBufferString(good), "gpu_sync", "00000000000000000000000000000238", at)
	require.NoError(t, err)
	require.EqualValues(t, 2, result.Nodes)
	require.EqualValues(t, 3, result.Edges)
	require.EqualValues(t, 5, result.Objects)
	require.EqualValues(t, 1, result.Skipped)
	require.Equal(t, 1, result.Reasons["gpu_missing_parts_sn"])
	for _, bad := range []string{`{"status":"succeeded","run_id":"abcdefabcdefabcdefabcdefabcdefab"}`, `{"status":"PASS","source_id":"different"}`, good + ` {"status":"PASS"}`, `not-json`} {
		_, err := roundStageResult(bytes.NewBufferString(bad), "gpu_sync", "00000000000000000000000000000238", at)
		require.Error(t, err)
	}
	_, err = roundStageResult(bytes.NewBufferString(good), "gpu_sync", "00000000000000000000000000000238", at.Add(time.Second))
	require.Error(t, err)
}

func TestQueueRoundModesRejectOtherActions(t *testing.T) {
	require.True(t, validQueuedRoundModes(true, false, nil, "", "", "", ""))
	require.True(t, validQueuedRoundModes(true, true, nil, "", "", "", ""))
	require.False(t, validQueuedRoundModes(true, false, []bool{true}, "", "", "", ""))
	require.False(t, validQueuedRoundModes(true, false, nil, "source", "", "", ""))
	require.False(t, validQueuedRoundModes(true, false, nil, "", "", "", "time"))
}

func TestFailedGPUPhasePreservesKnownCounts(t *testing.T) {
	err := fmt.Errorf("execution: %w", &service.GPUSyncError{Code: "gpu_uplink_write_failed", Stage: "uplink_write", Details: map[string]any{"run_id": "abcdefabcdefabcdefabcdefabcdefab", "nodes": 20197, "edges": 25538, "objects": 28738, "skipped": 3200, "missing_endpoints": 2588}})
	result := failedGPUPhaseResult(err)
	require.Equal(t, "abcdefabcdefabcdefabcdefabcdefab", result.RunID)
	require.Equal(t, "gpu_uplink_write_failed", result.ErrorCode)
	require.EqualValues(t, 20197, result.Nodes)
	require.EqualValues(t, 25538, result.Edges)
	require.Empty(t, failedGPUPhaseResult(fmt.Errorf("untyped failure")).RunID)
}
