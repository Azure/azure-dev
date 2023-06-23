package appdetect

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/*
var testDataFs embed.FS

func TestDetect(t *testing.T) {
	dotnetProj := Project{
		Language:      DotNet,
		Path:          "dotnet",
		DetectionRule: "Inferred by presence of: Program.cs, dotnettestapp.csproj",
	}

	tests := []struct {
		name    string
		options []DetectOption
		want    []Project
	}{
		{
			"Full",
			nil,
			[]Project{
				dotnetProj,
				{
					Language:      Java,
					Path:          "java",
					DetectionRule: "Inferred by presence of: pom.xml",
				},
				{
					Language:      JavaScript,
					Path:          "javascript",
					DetectionRule: "Inferred by presence of: package.json",
				},
				{
					Language:      JavaScript,
					Path:          "javascript-frameworks",
					DetectionRule: "Inferred by presence of: package.json",
					Frameworks: []Framework{
						Angular,
						JQuery,
						React,
						VueJs,
					},
				},
				{
					Language:      Python,
					Path:          "python",
					DetectionRule: "Inferred by presence of: requirements.txt",
				},
				{
					Language:      TypeScript,
					Path:          "typescript",
					DetectionRule: "Inferred by presence of: package.json",
				},
			},
		},
		{
			"WithProjectType",
			[]DetectOption{WithProjectType(DotNet)},
			[]Project{dotnetProj},
		},
		{
			"WithoutProjectTypes",
			[]DetectOption{
				WithProjectType(DotNet),
				WithProjectType(Java),
				WithoutProjectType(Java)},
			[]Project{dotnetProj},
		},
		{
			"WithIncludePatterns",
			[]DetectOption{WithIncludePatterns([]string{"**/dotnet"})},
			[]Project{dotnetProj},
		},
		{
			"WithExcludePatterns",
			[]DetectOption{
				WithIncludePatterns([]string{"**/dotnet", "**/java"}),
				WithExcludePatterns([]string{"**/java"}, true),
			},
			[]Project{dotnetProj},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := copyTestDataDir(t, "**", dir)
			require.NoError(t, err)
			projects, err := Detect(dir, tt.options...)
			require.NoError(t, err)

			// Convert relative to absolute paths
			for i := range tt.want {
				tt.want[i].Path = filepath.Join(dir, tt.want[i].Path)
			}

			require.Equal(t, tt.want, projects)
		})
	}
}

func copyTestDataDir(t *testing.T, glob string, dst string) error {
	root := "testdata"
	return fs.WalkDir(testDataFs, root, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(dst, name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(testDataFs, name)
		if err != nil {
			return err
		}

		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
