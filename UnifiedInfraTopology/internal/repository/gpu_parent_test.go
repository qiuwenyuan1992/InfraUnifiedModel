package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
	"testing"
)

func TestGPUParentRequiresActualSameSourceGPUCategory(t *testing.T) {
	for _, failure := range []string{"", "missing", "wrong-source", "wrong-SN", "wrong-parent", "wrong-device"} {
		t.Run(failure, func(t *testing.T) {
			source, sn := "s", "GPU-SERVER"
			parent, kind := int64(52), int64(58)
			switch failure {
			case "wrong-source":
				source = "other"
			case "wrong-SN":
				sn = "other"
			case "wrong-parent":
				parent = 54
			case "wrong-device":
				kind = 57
			}
			rows := []*nebula.Row{{Values: []*nebula.Value{{SVal: []byte(source)}, {SVal: []byte(sn)}, {IVal: &parent}, {IVal: &kind}}}}
			if failure == "missing" {
				rows = nil
			}
			result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: &nebula.DataSet{ColumnNames: [][]byte{[]byte("source_id"), []byte("device_sn"), []byte("parent_type_id"), []byte("device_type_id")}, Rows: rows}})
			require.NoError(t, err)
			repo := &topologyGraphRepository{client: &topologyGraphFake{results: []*nebulago.ResultSet{result}}}
			ok, err := repo.ParentGPUDeviceValid(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityDevice, StableID: "GPU-SERVER"})
			require.NoError(t, err)
			require.Equal(t, failure == "", ok)
		})
	}
	_, err := (&topologyGraphRepository{}).ParentGPUDeviceValid(context.Background(), model.EntityIdentity{SourceID: "s", EntityType: model.EntityGPU, StableID: "GPU"})
	require.Error(t, err)
}
