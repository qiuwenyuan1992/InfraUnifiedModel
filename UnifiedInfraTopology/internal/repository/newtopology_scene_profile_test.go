package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"UnifiedInfraTopology/internal/model"
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

type sceneProfileClient struct {
	graphclient.Client
	t       *testing.T
	queries int
}

func (c *sceneProfileClient) ExecuteParameter(ctx context.Context, statement string, parameters map[string]interface{}) (*nebulago.ResultSet, error) {
	require.True(c.t, strings.HasPrefix(statement, "MATCH "), "profiling only permits read-only MATCH statements")
	c.queries++
	label := "node"
	if strings.Contains(statement, "$after") {
		label = "children"
	} else if relation, ok := parameters["relation_kind"].(string); ok {
		label = relation
		if strings.Contains(statement, "MATCH (b)-[e:") {
			label += "_in"
		} else if strings.Contains(statement, "MATCH (a)-[e:") {
			label += "_out"
		}
	} else if _, ok := parameters["vids"]; ok {
		label = "peer_nodes"
	}
	began := time.Now()
	result, err := c.Client.ExecuteParameter(ctx, statement, parameters)
	rows, success := 0, false
	if result != nil {
		rows, success = result.GetRowSize(), result.IsSucceed()
	}
	c.t.Logf("query=%d label=%s duration=%s rows=%d success=%t transport_error=%t", c.queries, label, time.Since(began), rows, success, err != nil)
	return result, err
}

func TestLivePOD248SceneNetworkReadOnlyProfile(t *testing.T) {
	path := os.Getenv("COMPACT_SCENE_READ_CONFIG")
	if path == "" {
		t.Skip("显式配置后只读分析 POD248，不同步或写图")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	var client *sceneProfileClient
	repo, cleanup, err := newTopologyReadRepository(conf, func(config graphclient.Config) (graphclient.Client, error) {
		underlying, err := graphclient.New(config)
		if err != nil {
			return nil, err
		}
		client = &sceneProfileClient{Client: underlying, t: t}
		return client, nil
	})
	require.NoError(t, err)
	defer cleanup()
	require.NotNil(t, client, "configured read client must connect")
	ctx := WithTopologyQueryBudget(context.Background(), 40)
	identity := model.EntityIdentity{SourceID: "00000000000000000000000000000238", EntityType: model.EntityPod, StableID: "4f605d51-267c-4f86-9227-5f37828fefac"}
	_, err = repo.Node(ctx, identity)
	require.NoError(t, err)
	children, err := repo.Children(ctx, identity, TopologyDirectoryQuery{Limit: 5})
	require.NoError(t, err)
	require.Len(t, children.Items, 5)
	began := time.Now()
	page, err := repo.(TopologySceneNetworkReader).SceneNetwork(ctx, children.Items, 600)
	duration := time.Since(began)
	require.NoError(t, err)
	t.Logf("scene_network duration=%s seeds=%d nodes=%d edges=%d truncated=%t", duration, len(children.Items), len(page.Nodes), len(page.Edges), page.Truncated)
}
