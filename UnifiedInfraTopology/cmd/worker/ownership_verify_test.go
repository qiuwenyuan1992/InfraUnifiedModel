package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
	"github.com/vesoft-inc/nebula-go/v3/nebula/graph"
)

type ownershipTestClient struct {
	t       *testing.T
	at      time.Time
	failure string
}

func (*ownershipTestClient) Close() {}
func (c *ownershipTestClient) ExecuteParameter(ctx context.Context, query string, params map[string]interface{}) (*nebulago.ResultSet, error) {
	require.Equal(c.t, "source", params["source_id"])
	require.Equal(c.t, "owns_interface", params["relation_kind"])
	if c.failure == "query" {
		return nil, errors.New("unavailable")
	}
	identity := model.RelationIdentity{SourceID: "source", EdgeType: model.EdgeComposition, Kind: model.RelationOwnsInterface, From: model.EntityIdentity{SourceID: "source", EntityType: model.EntityDevice, StableID: "SN"}, To: model.EntityIdentity{SourceID: "source", EntityType: model.EntityInterface, StableID: "PORT"}}
	id, _ := identity.ID()
	rank, _ := identity.Rank()
	at := c.at.Format(time.RFC3339Nano)
	var rows [][]any
	switch {
	case strings.Contains(query, "LOOKUP") && strings.Contains(query, "count(*)"):
		rows = [][]any{{int64(1)}}
	case strings.Contains(query, "LOOKUP"):
		if c.failure == "timestamp" {
			at = c.at.Add(time.Second).Format(time.RFC3339Nano)
		}
		rows = [][]any{{"src", "dst", rank, id, c.at.Format(time.RFC3339Nano), at}}
		if c.failure == "count" {
			rows = nil
		}
		if c.failure == "duplicate" {
			rows = append(rows, rows[0])
		}
		if c.failure == "multiple owners" {
			rows = append(rows, []any{"other-source-vid", "dst", rank + 1, "other-relation", c.at.Format(time.RFC3339Nano), at})
		}
	case strings.Contains(query, "count(e)"):
		count := int64(1)
		if c.failure == "endpoint" {
			count = 0
		}
		rows = [][]any{{count}}
	default:
		if c.failure == "rank" {
			rank++
		}
		if c.failure == "identity" {
			id = "wrong"
		}
		rows = [][]any{{"SN", "PORT", id, rank, at}}
	}
	columns := 1
	if len(rows) > 0 {
		columns = len(rows[0])
	} else if strings.Contains(query, "LOOKUP") {
		columns = 6
	}
	data := &nebula.DataSet{}
	for index := 0; index < columns; index++ {
		data.ColumnNames = append(data.ColumnNames, []byte(fmt.Sprint(index)))
	}
	for _, row := range rows {
		r := &nebula.Row{}
		for _, value := range row {
			switch v := value.(type) {
			case string:
				r.Values = append(r.Values, &nebula.Value{SVal: []byte(v)})
			case int64:
				x := v
				r.Values = append(r.Values, &nebula.Value{IVal: &x})
			}
		}
		data.Rows = append(data.Rows, r)
	}
	return nebulago.GenResultSet(&graph.ExecutionResponse{ErrorCode: nebula.ErrorCode_SUCCEEDED, Data: data})
}
func TestOwnershipVerificationRejectsFalseSuccess(t *testing.T) {
	at := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	for _, failure := range []string{"", "query", "count", "timestamp", "duplicate", "multiple owners", "endpoint", "rank", "identity"} {
		t.Run(failure, func(t *testing.T) {
			expected := int64(1)
			if failure == "duplicate" || failure == "multiple owners" {
				expected = 2
			}
			r, err := verifyOwnership(context.Background(), &ownershipTestClient{t: t, at: at, failure: failure}, "source", at, expected)
			if failure != "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(1), r.ValidEndpointCount)
				require.Len(t, r.Samples, 1)
			}
		})
	}
}

func TestOwnershipVerificationQueryIsScopedAndReadOnly(t *testing.T) {
	query := ownershipGraphQuery(true)
	require.Contains(t, query, "LOOKUP ON composition_relation")
	require.Contains(t, query, "composition_relation.source_id == $source_id")
	require.Contains(t, query, "composition_relation.relation_kind == $relation_kind")
	require.Contains(t, query, "datetime($snapshot)")
	require.NotContains(t, query, "DELETE")
	require.Contains(t, query, "rank(edge)")
}
