package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainPreservesInitializationError(t *testing.T) {
	// 无效空间名在连接图库前失败，SQLite 仅使用进程内存。
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"data": {"db": {"main": {"driver": "sqlite", "dsn": ":memory:"}}},
		"log": {"mode": "console"},
		"inventory": {"graph": {"space": "invalid-space!"}}
	}`), 0600))
	t.Setenv("APP_CONF", path)
	oldFlags, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldFlags, oldArgs })
	flag.CommandLine = flag.NewFlagSet("server", flag.ContinueOnError)
	os.Args = []string{"server"}
	require.PanicsWithError(t, "invalid inventory.graph.space", main)
}
