package repository

import (
	"UnifiedInfraTopology/internal/model"
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
	"testing"
)

func TestServerUplinkEndpointsRequireUniqueCorrectTypedOwner(t *testing.T) {
	server := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "SERVER"}
	tor := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "TOR"}
	port := model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "PORT"}
	for _, failure := range []string{"", "absent", "multiple", "wrong tor", "wrong server type", "wrong tor type", "wrong port type", "wrong source"} {
		t.Run(failure, func(t *testing.T) {
			values := []any{"SERVER", int64(52), "source", "TOR", int64(54), "PORT", "physics"}
			switch failure {
			case "wrong tor":
				values[3] = "other"
			case "wrong server type":
				values[1] = int64(54)
			case "wrong tor type":
				values[4] = int64(52)
			case "wrong port type":
				values[6] = "virtual"
			case "wrong source":
				values[2] = "other"
			}
			row := &nebula.Row{}
			for _, value := range values {
				switch v := value.(type) {
				case string:
					row.Values = append(row.Values, &nebula.Value{SVal: []byte(v)})
				case int64:
					x := v
					row.Values = append(row.Values, &nebula.Value{IVal: &x})
				}
			}
			data := &nebula.DataSet{}
			for index := range values {
				data.ColumnNames = append(data.ColumnNames, []byte(fmt.Sprint(index)))
			}
			if failure != "absent" {
				data.Rows = append(data.Rows, row)
			}
			if failure == "multiple" {
				data.Rows = append(data.Rows, row)
			}
			result, err := nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
			require.NoError(t, err)
			repo := &topologyGraphRepository{client: &topologyGraphFake{results: []*nebulago.ResultSet{result}}}
			ok, err := repo.ServerUplinkEndpointsValid(context.Background(), server, tor, port)
			require.NoError(t, err)
			require.Equal(t, failure == "", ok)
		})
	}
}

func TestServerUplinkEndpointQueryRejectsWrongIdentityBeforeRead(t *testing.T) {
	server := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "SERVER"}
	tor := model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "TOR"}
	port := model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "PORT"}
	repo := &topologyGraphRepository{}
	wrong := port
	wrong.SourceID = "different"
	ok, err := repo.ServerUplinkEndpointsValid(context.Background(), server, tor, wrong)
	require.Error(t, err)
	require.False(t, ok)
	wrong = server
	wrong.EntityType = model.EntityGPU
	ok, err = repo.ServerUplinkEndpointsValid(context.Background(), wrong, tor, port)
	require.Error(t, err)
	require.False(t, ok)
	query, params, err := serverUplinkEndpointQuery(server, tor, port)
	require.NoError(t, err)
	require.Contains(t, query, "owns_interface")
	require.Contains(t, query, "id(p) == $port_vid")
	require.Contains(t, query, "parent_type_id")
	require.NotContains(t, query, "SERVER")
	require.Equal(t, "source", params["source_id"])
}
