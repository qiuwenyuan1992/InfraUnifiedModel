package nebula

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	nebulago "github.com/vesoft-inc/nebula-go/v3"
)

func TestParseConfig(t *testing.T) {
	for _, test := range []struct {
		name, key string
		value     interface{}
	}{
		{"hosts scalar", "hosts", "localhost:9669"}, {"host type", "hosts", []interface{}{1}},
		{"host empty", "hosts", []string{":9669"}}, {"host no port", "hosts", []string{"localhost"}},
		{"host port zero", "hosts", []string{"localhost:0"}}, {"host port high", "hosts", []string{"localhost:65536"}},
		{"space injection", "space", "current`; DROP SPACE other;"}, {"space type", "space", 123},
		{"timeout zero", "timeout_seconds", 0}, {"timeout negative", "timeout_seconds", -1},
		{"timeout high", "timeout_seconds", 61}, {"timeout fraction", "timeout_seconds", 1.5},
		{"timeout text", "timeout_seconds", "invalid"}, {"username type", "username", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			conf := viper.New()
			conf.Set("inventory.graph."+test.key, test.value)
			_, err := ParseConfig(conf)
			require.Error(t, err)
		})
	}
	conf := viper.New()
	conf.Set("inventory.graph.hosts", []interface{}{"localhost:9669", "[::1]:9669"})
	c, err := ParseConfig(conf)
	require.NoError(t, err)
	require.Empty(t, c.Space)
	conf.Set("inventory.graph.space", "current_inventory")
	_, err = ParseConfig(conf)
	require.Error(t, err)
	conf.Set("inventory.graph.username", "reader")
	conf.Set("inventory.graph.password", "test-password")
	c, err = ParseConfig(conf)
	require.NoError(t, err)
	require.Len(t, c.Hosts, 2)
	require.Equal(t, 5*time.Second, c.Timeout)
	conf.Set("inventory.graph.timeout_seconds", 2)
	c, err = ParseConfig(conf)
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, c.Timeout)
}

type fakeSessionPool struct {
	calls, closes int
	after         func()
}

func (p *fakeSessionPool) ExecuteWithParameter(string, map[string]interface{}) (*nebulago.ResultSet, error) {
	p.calls++
	if p.after != nil {
		p.after()
	}
	return nil, nil
}
func (p *fakeSessionPool) Close() { p.closes++ }

func TestClientCancellationAndCleanup(t *testing.T) {
	pool := &fakeSessionPool{}
	c := &client{pool: pool}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ExecuteParameter(ctx, "query", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, pool.calls)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	pool.after = cancel
	_, err = c.ExecuteParameter(ctx, "query", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, pool.calls)
	c.Close()
	c.Close()
	require.Equal(t, 1, pool.closes)
	_, err = c.ExecuteParameter(context.Background(), "query", nil)
	require.Error(t, err)
	require.Equal(t, 1, pool.calls)
}
