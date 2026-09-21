package migration

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProductContractHasNoRangeIdentity(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	upstreamField := "in_monitor_" + "scope" + "_id"
	forbidden := regexp.MustCompile(`\b` + `scope` + `_id\b|\bScope` + `ID\b|\bscope` + `ID\b|\bTopology` + `Scope\b|\btopology_` + `scopes\b|/scopes(?:/|\b)`)
	extensions := map[string]bool{
		".go": true, ".sql": true, ".md": true, ".yml": true, ".yaml": true, ".json": true,
	}
	var matches []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || !extensions[filepath.Ext(path)] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for lineNumber, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, upstreamField) {
				continue
			}
			if forbidden.MatchString(line) {
				relative, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				matches = append(matches, relative+":"+strconv.Itoa(lineNumber+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(matches)
	if len(matches) > 0 {
		t.Fatalf("topology scope contract remains in:\n%s", strings.Join(matches, "\n"))
	}
}
