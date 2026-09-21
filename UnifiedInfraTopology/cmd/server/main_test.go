package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainPreservesInitializationError(t *testing.T) {
	// 无效数据库驱动在 HTTP server 启动前稳定触发初始化失败。
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
			"data": {"db": {"main": {"driver": "invalid", "dsn": ":memory:"}}},
			"log": {"mode": "console"}
		}`), 0600))

	t.Setenv("APP_CONF", path)
	oldFlags, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldFlags, oldArgs })
	flag.CommandLine = flag.NewFlagSet("server", flag.ContinueOnError)
	os.Args = []string{"server"}
	require.PanicsWithValue(t, "unknown db driver", main)
}
