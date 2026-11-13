package main

import (
	graphclient "UnifiedInfraTopology/pkg/nebula"
	"context"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveRoomSchema(t *testing.T) {
	path := os.Getenv("ROOM_SCHEMA_CONFIG")
	if path == "" {
		t.Skip("显式配置只读检查，独立令牌才允许room增量DDL")
	}
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	require.Equal(t, "unified_infra_topology", cfg.Space)
	for _, host := range cfg.Hosts {
		require.Equal(t, "127.0.0.1", host.Host)
	}
	c, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	execute := func(q string) [][]string {
		r, e := c.ExecuteParameter(ctx, q, nil)
		require.NoError(t, e)
		require.NotNil(t, r)
		require.True(t, r.IsSucceed(), "%s: %s", q, r.GetErrorMsg())
		return r.AsStringTable()
	}
	tags := execute("SHOW TAGS;")
	rpp := execute("SHOW CREATE TAG rpp;")
	exists := false
	for _, row := range tags[1:] {
		for _, name := range row {
			if strings.Trim(name, "\"") == "room" {
				exists = true
			}
		}
	}
	t.Logf("room exists=%v rpp.idc_room exists=%v", exists, strings.Contains(rpp[1][1], "idc_room"))
	if os.Getenv("ROOM_SCHEMA_CONFIRM") == "" {
		return
	}
	require.Equal(t, "ADD_ROOM_ONLY", os.Getenv("ROOM_SCHEMA_CONFIRM"))
	require.False(t, exists)
	require.NotContains(t, rpp[1][1], "idc_room")
	data, e := os.ReadFile("../../internal/migration/nebula/0005_room.ngql")
	require.NoError(t, e)
	for _, stmt := range strings.Split(string(data), ";") {
		lines := []string{}
		for _, line := range strings.Split(stmt, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		q := strings.TrimSpace(strings.Join(lines, "\n"))
		if q != "" {
			execute(q + ";")
		}
	}
	t.Logf("room schema=%v", execute("SHOW CREATE TAG room;"))
	execute("SHOW CREATE TAG INDEX idx_room_identity;")
	require.Contains(t, execute("SHOW CREATE TAG rpp;")[1][1], "idc_room")
}

// 本地开发图直接补目录计数属性（与 0006 增量一致），不清理任何数据。
func TestLiveDirectoryCountsSchema(t *testing.T) {
	path := os.Getenv("DIRECTORY_COUNTS_SCHEMA_CONFIG")
	if path == "" {
		t.Skip("显式配置后为开发图补 room/data_center 目录计数属性")
	}
	require.Equal(t, "ADD_DIRECTORY_COUNTS", os.Getenv("DIRECTORY_COUNTS_SCHEMA_CONFIRM"))
	conf := viper.New()
	conf.SetConfigFile(path)
	require.NoError(t, conf.ReadInConfig())
	cfg, err := graphclient.ParseConfig(conf)
	require.NoError(t, err)
	require.Equal(t, "unified_infra_topology", cfg.Space)
	for _, host := range cfg.Hosts {
		require.Equal(t, "127.0.0.1", host.Host)
	}
	c, err := graphclient.New(cfg)
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	execute := func(q string) [][]string {
		r, e := c.ExecuteParameter(ctx, q, nil)
		require.NoError(t, e)
		require.NotNil(t, r)
		require.True(t, r.IsSucceed(), "%s: %s", q, r.GetErrorMsg())
		return r.AsStringTable()
	}
	for _, tag := range []string{"room", "data_center", "cabinet"} {
		if strings.Contains(execute("SHOW CREATE TAG " + tag + ";")[1][1], "server_num") {
			t.Logf("%s 已有目录计数属性，跳过 ALTER", tag)
			continue
		}
		columns := "cabinet_num INT NULL, server_num INT NULL, network_device_num INT NULL"
		if tag == "cabinet" {
			columns = "device_num INT NULL, server_num INT NULL, network_device_num INT NULL"
		}
		execute("ALTER TAG " + tag + " ADD (" + columns + ");")
	}
	// ALTER 异步传播，等全部 Tag 都确认新属性后再执行同步。
	deadline := time.Now().Add(20 * time.Second)
	for _, tag := range []string{"room", "data_center", "cabinet"} {
		for !strings.Contains(execute("SHOW CREATE TAG " + tag + ";")[1][1], "server_num") {
			require.True(t, time.Now().Before(deadline), "%s 目录计数属性未在期限内传播", tag)
			time.Sleep(time.Second)
		}
	}
	t.Log("room/data_center/cabinet 目录计数属性已就绪")
}
