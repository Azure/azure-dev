// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func moveFile(t *testing.T, src, dst string) string {
	err := os.MkdirAll(filepath.Dir(dst), 0700)
	require.NoError(t, err)

	err = os.Rename(src, dst)
	require.NoError(t, err)

	return dst
}

func TestFindFastApiMain(t *testing.T) {
	temp := t.TempDir()

	contents, err := testDataFs.ReadFile("testdata/assets/fastapi.py")
	require.NoError(t, err)

	dst := filepath.Join(temp, "main.py")
	err = os.WriteFile(dst, contents, 0600)
	require.NoError(t, err)
	s, err := PyFastApiLaunch(temp)
	require.NoError(t, err)
	require.Equal(t, "main:app", s)

	dst = moveFile(t, dst, filepath.Join(temp, "myapp", "main.py"))
	s, err = PyFastApiLaunch(temp)
	require.NoError(t, err)
	require.Equal(t, "myapp.main:app", s)

	dst = moveFile(t, dst, filepath.Join(temp, "src", "myapp", "app.py"))
	s, err = PyFastApiLaunch(temp)
	require.NoError(t, err)
	require.Equal(t, "src.myapp.app:app", s)

	_ = moveFile(t, dst, filepath.Join(temp, "src", "myapp", "toodeep", "main.py"))
	s, err = PyFastApiLaunch(temp)
	require.NoError(t, err)
	require.Empty(t, s)
}

func TestDetectPythonProject(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string // filename -> content
		wantNil    bool
		wantRule   string
		wantDeps   []Dependency
		wantDbDeps []DatabaseDep
	}{
		{
			name:    "NoDependencyFiles",
			files:   map[string]string{"main.py": "print('hello')"},
			wantNil: true,
		},
		{
			name:     "RequirementsTxtOnly",
			files:    map[string]string{"requirements.txt": "fastapi==0.101.1\n"},
			wantRule: "Inferred by presence of: requirements.txt",
			wantDeps: []Dependency{PyFastApi},
		},
		{
			name: "PyprojectTomlOnly",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"myapp\"\ndependencies = [\n    \"fastapi>=0.101.1\",\n]\n",
			},
			wantRule: "Inferred by presence of: pyproject.toml",
			wantDeps: []Dependency{PyFastApi},
		},
		{
			name: "PyprojectTomlPrioritizedOverRequirementsTxt",
			files: map[string]string{
				"requirements.txt": "flask==2.3.2\n",
				"pyproject.toml":   "[project]\nname = \"myapp\"\ndependencies = [\n    \"fastapi>=0.101.1\",\n]\n",
			},
			wantRule: "Inferred by presence of: pyproject.toml",
			wantDeps: []Dependency{PyFastApi},
		},
		{
			name: "PyprojectTomlFrameworkDeps",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"myapp\"\ndependencies = [\n" +
					"    \"fastapi>=0.101.1\",\n" +
					"    \"flask~=2.3\",\n" +
					"    \"django!=4.0\",\n" +
					"]\n",
			},
			wantRule: "Inferred by presence of: pyproject.toml",
			wantDeps: []Dependency{PyDjango, PyFastApi, PyFlask},
		},
		{
			name: "PyprojectTomlDatabaseDeps",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"myapp\"\ndependencies = [\n" +
					"    \"psycopg2-binary>=3.0\",\n" +
					"    \"pymongo>=4.0\",\n" +
					"    \"redis>=4.0\",\n" +
					"    \"mysqlclient>=2.0\",\n" +
					"]\n",
			},
			wantRule:   "Inferred by presence of: pyproject.toml",
			wantDbDeps: []DatabaseDep{DbMongo, DbMySql, DbPostgres, DbRedis},
		},
		{
			name: "RequirementsTxtDatabaseDeps",
			files: map[string]string{
				"requirements.txt": "psycopg2==3.0\npymongo==4.0\nredis==4.0\nmysqlclient==2.0\n",
			},
			wantRule:   "Inferred by presence of: requirements.txt",
			wantDbDeps: []DatabaseDep{DbMongo, DbMySql, DbPostgres, DbRedis},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
				require.NoError(t, err)
			}

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)

			detector := &pythonDetector{}
			project, err := detector.DetectProject(context.Background(), dir, entries)
			require.NoError(t, err)

			if tt.wantNil {
				require.Nil(t, project)
				return
			}

			require.NotNil(t, project)
			require.Equal(t, Python, project.Language)
			require.Equal(t, dir, project.Path)
			require.Equal(t, tt.wantRule, project.DetectionRule)
			require.Equal(t, tt.wantDeps, project.Dependencies)
			if tt.wantDbDeps != nil {
				require.Equal(t, tt.wantDbDeps, project.DatabaseDeps)
			}
		})
	}
}
