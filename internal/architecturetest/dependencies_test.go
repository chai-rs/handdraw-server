// Package architecturetest enforces the domain and application dependency boundaries from D15.
package architecturetest_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const module = "github.com/chai-rs/handdraw-server/"

func forbidden(source, dependency string) bool {
	source = filepath.ToSlash(source)
	local := strings.TrimPrefix(dependency, module)
	parts := strings.Split(source, "/")
	if strings.HasPrefix(source, "pkg/") && (strings.HasPrefix(local, "internal/") || strings.HasPrefix(local, "app/")) {
		return true
	}
	if len(parts) > 2 && parts[0] == "internal" {
		if strings.HasPrefix(local, "app/") {
			return true
		}
		if strings.HasPrefix(local, "internal/") && strings.Split(local, "/")[1] != parts[1] {
			return true
		}
	}
	if strings.Contains(source, "/service/") || strings.Contains(source, "/inbound/") {
		if strings.Contains(local, "/infra/") || dependency == "database/sql" || strings.Contains(dependency, "github.com/uptrace/bun") || strings.HasPrefix(local, "pkg/bun") {
			return true
		}
	}
	return false
}

// TestProductionDependencies walks source imports independently of currently selected build tags.
func TestProductionDependencies(t *testing.T) {
	root := "../.."
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(filepath.ToSlash(relative), "internal/testsupport/") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			require.False(t, forbidden(relative, dependency), "%s imports forbidden dependency %s", relative, dependency)
		}
		return nil
	}))
}

// TestDependencyRules catches cross-domain, inverted shared-package and concrete service persistence imports.
func TestDependencyRules(t *testing.T) {
	for _, tc := range []struct {
		source, dependency string
		deny               bool
	}{
		{"internal/board/service/main.go", module + "internal/identity/model", true},
		{"internal/board/service/main.go", module + "internal/board/model", false},
		{"internal/board/service/main.go", module + "app/access/model", true},
		{"app/board_management/service/main.go", module + "internal/board/infra/db", true},
		{"app/board_management/infra/db/main.go", "github.com/uptrace/bun", false},
		{"app/board_management/service/main.go", module + "app/access/model", false},
		{"pkg/shared/main.go", module + "internal/board/model", true},
		{"internal/board/service/main.go", "database/sql", true},
	} {
		t.Run(tc.source+"/"+tc.dependency, func(t *testing.T) { require.Equal(t, tc.deny, forbidden(tc.source, tc.dependency)) })
	}
}
