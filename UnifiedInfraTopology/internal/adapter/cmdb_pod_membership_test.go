package adapter

import (
	"context"
	"testing"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
)

func membershipTestCMDB(t *testing.T, devices, pods []map[string]any, calls *int) *CMDB {
	t.Helper()
	return newPodPassTestCMDB(t, func(req interfacePassRequest) any {
		require.Equal(t, "inst_id", req.body.Page.Sort)
		items := devices
		switch req.path {
		case deviceViewEndpoint:
			require.Equal(t, map[string]any{"idc_id": float64(238)}, req.body.Condition)
			for _, field := range []string{"inst_id", "device_sn", "pod_uuid", "pod_id", "pod_name", "compute_plane"} {
				require.Contains(t, req.body.Fields, field)
			}
		case "/pub/api/v3/find/instance/object/pod":
			require.Empty(t, req.body.Condition)
			items = pods
			if calls != nil {
				*calls++
			}
		default:
			t.Fatalf("membership must not read cabinet or write nodes: %s", req.path)
		}
		end := min(req.body.Page.Start+req.body.Page.Limit, len(items))
		return interfacePassEnvelope(len(items), items[req.body.Page.Start:end])
	})
}

func TestPODMembershipPassResolvesManagementFallbackAndCompute(t *testing.T) {
	devices := []map[string]any{
		{"inst_id": 1, "device_sn": "sn-a", "pod_id": 10, "compute_plane": []any{map[string]any{"pod_id": 20}, map[string]any{"pod_id": 20}}},
		{"inst_id": 2, "device_sn": "sn-b", "pod_uuid": " pod-main ", "pod_id": 10},
		{"inst_id": 3, "device_sn": "sn-c"},
	}
	pods := []map[string]any{{"inst_id": 10, "uuid": "pod-main", "plane": 1}, {"inst_id": 20, "uuid": "pod-compute", "plane": 2}}
	calls := 0
	c := membershipTestCMDB(t, devices, pods, &calls)
	pass, err := c.OpenPODMembershipPass(context.Background(), model.Source{ID: "s", ConfigRef: "primary"})
	require.NoError(t, err)
	before := calls
	page, err := pass.Page(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, 3, page.Total)
	require.Len(t, page.Relations, 3)
	require.Equal(t, int64(1), page.Relations[0].Properties["plane"])
	require.Equal(t, "1", page.Relations[0].Identity.Discriminator)
	require.Equal(t, int64(2), page.Relations[1].Properties["plane"])
	require.Equal(t, "s", page.Relations[0].Identity.To.SourceID)
	require.False(t, page.Done)
	last, err := pass.Page(context.Background(), page.NextOffset)
	require.NoError(t, err)
	require.True(t, last.Done)
	require.Zero(t, last.Skipped)
	require.Equal(t, 1, last.Reasons["no_pod_references"])
	require.Equal(t, before, calls, "catalog is resolved once per pass")
	require.NotEmpty(t, page.SourceDigest)
	require.Equal(t, pass.ScopeDigest(), page.SourceDigest)
}

func TestPODMembershipPassSkipsContradictionsMissingPlaneAndMissingTargets(t *testing.T) {
	devices := []map[string]any{
		{"inst_id": 1, "device_sn": "a", "pod_uuid": "one", "pod_id": 20},
		{"inst_id": 2, "device_sn": "b", "pod_id": 30},
		{"inst_id": 3, "device_sn": "c", "compute_plane": []any{map[string]any{"pod_id": 10}}},
		{"inst_id": 4, "device_sn": "d", "pod_id": 404},
		{"inst_id": 5, "device_sn": "e", "pod_id": 40},
	}
	pods := []map[string]any{{"inst_id": 10, "uuid": "one", "plane": 1}, {"inst_id": 20, "uuid": "two", "plane": 2}, {"inst_id": 30, "uuid": "three"}, {"inst_id": 40, "uuid": "four", "plane": 99}}
	c := membershipTestCMDB(t, devices, pods, nil)
	pass, err := c.OpenPODMembershipPass(context.Background(), model.Source{ID: "s", ConfigRef: "primary"})
	require.NoError(t, err)
	var relations []model.Relation
	skipped := 0
	reasons := map[string]int{}
	for start := 0; ; {
		page, err := pass.Page(context.Background(), start)
		require.NoError(t, err)
		relations = append(relations, page.Relations...)
		skipped += page.Skipped
		for k, n := range page.Reasons {
			reasons[k] += n
		}
		if page.Done {
			break
		}
		start = page.NextOffset
	}
	require.Len(t, relations, 1)
	require.Equal(t, int64(99), relations[0].Properties["plane"])
	require.Equal(t, 4, skipped)
	for _, reason := range []string{"uuid_id_conflict", "missing_plane", "compute_plane_conflict", "missing_pod", "unknown_plane"} {
		require.Equal(t, 1, reasons[reason], reason)
	}
}

func TestPODMembershipPassRejectsDuplicateSNAndDigestIncludesPlane(t *testing.T) {
	devices := []map[string]any{{"inst_id": 1, "device_sn": "same", "pod_id": 10}, {"inst_id": 2, "device_sn": "same"}}
	pods := []map[string]any{{"inst_id": 10, "uuid": "one", "plane": 1}}
	c := membershipTestCMDB(t, devices, pods, nil)
	_, err := c.OpenPODMembershipPass(context.Background(), model.Source{ID: "s", ConfigRef: "primary"})
	require.Error(t, err)
	devices[1]["device_sn"] = "other"
	first, err := c.OpenPODMembershipPass(context.Background(), model.Source{ID: "s", ConfigRef: "primary"})
	require.NoError(t, err)
	pods[0]["plane"] = 99
	second, err := c.OpenPODMembershipPass(context.Background(), model.Source{ID: "s", ConfigRef: "primary"})
	require.NoError(t, err)
	require.NotEqual(t, first.ScopeDigest(), second.ScopeDigest())
}
