package maven

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_getMavenPath(t *testing.T) {
	rootPath := os.TempDir()
	sourcePath := filepath.Join(rootPath, "src")
	projectPath := filepath.Join(sourcePath, "api")
	azdMvn, _ := getAzdMvnCommand(downloadedMavenVersion)

	pathDir := os.TempDir()

	require.NoError(t, os.MkdirAll(projectPath, 0755))
	ostest.Unsetenv(t, "PATH")

	type args struct {
		projectPath     string
		rootProjectPath string
	}

	tests := []struct {
		name         string
		mvnwPath     []string
		mvnwRelative bool
		mvnPath      []string
		envVar       map[string]string
		want         string
		wantErr      bool
	}{
		{name: "MvnwProjectPath", mvnwPath: []string{projectPath}, want: filepath.Join(projectPath, mvnwWithExt())},
		{name: "MvnwSrcPath", mvnwPath: []string{sourcePath}, want: filepath.Join(sourcePath, mvnwWithExt())},
		{name: "MvnwRootPath", mvnwPath: []string{rootPath}, want: filepath.Join(rootPath, mvnwWithExt())},
		{
			name: "MvnwFirst", mvnwPath: []string{rootPath}, want: filepath.Join(rootPath, mvnwWithExt()),
			mvnPath: []string{pathDir}, envVar: map[string]string{"PATH": pathDir},
		},
		{
			name:         "MvnwProjectPathRelative",
			mvnwPath:     []string{projectPath},
			want:         filepath.Join(projectPath, mvnwWithExt()),
			mvnwRelative: true,
		},
		{
			name:         "MvnwSrcPathRelative",
			mvnwPath:     []string{sourcePath},
			want:         filepath.Join(sourcePath, mvnwWithExt()),
			mvnwRelative: true,
		},
		{
			name:         "MvnwRootPathRelative",
			mvnwPath:     []string{rootPath},
			want:         filepath.Join(rootPath, mvnwWithExt()),
			mvnwRelative: true,
		},
		{
			name:    "Mvn",
			mvnPath: []string{pathDir},
			envVar:  map[string]string{"PATH": pathDir},
			want:    filepath.Join(pathDir, mvnWithExt()),
		},
		{
			name: "Use azd downloaded maven",
			want: azdMvn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placeExecutable(t, mvnwWithExt(), tt.mvnwPath...)
			placeExecutable(t, mvnWithExt(), tt.mvnPath...)
			ostest.Setenvs(t, tt.envVar)

			args := args{}
			if tt.mvnwRelative {
				ostest.Chdir(t, rootPath)
				// Set PWD directly to avoid symbolic links

				t.Setenv("PWD", rootPath)
				projectPathRel, err := filepath.Rel(rootPath, projectPath)
				require.NoError(t, err)
				args.projectPath = projectPathRel
				args.rootProjectPath = ""
			} else {
				args.projectPath = projectPath
				args.rootProjectPath = rootPath
			}

			wd, err := os.Getwd()
			require.NoError(t, err)
			log.Printf("rootPath: %s, cwd: %s, getMavenPath(%s, %s)\n", rootPath, wd, args.projectPath,
				args.rootProjectPath)
			actual, err := getMavenPath(args.projectPath, args.rootProjectPath)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, actual)
		})
	}
}

func Test_extractVersion(t *testing.T) {
	execMock := mockexec.NewMockCommandRunner().
		When(func(a exec.RunArgs, command string) bool { return a.Args[0] == "--version" }).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, heredoc.Doc(`
			Apache Maven 3.9.1 (2e178502fcdbffc201671fb2537d0cb4b4cc58f8)
			Maven home: C:\Tools\apache-maven-3.9.1
			Java version: 17.0.6, vendor: Microsoft, runtime: C:\Program Files\Microsoft\jdk-17.0.6.10-hotspot
			Default locale: en_US, platform encoding: Cp1252
			OS name: "windows 11", version: "10.0", arch: "amd64", family: "windows"
			`), ""), nil
		})

	mvn := NewCli(execMock)
	placeExecutable(t, mvnwWithExt(), mvn.projectPath)
	ver, err := mvn.extractVersion(context.Background())
	require.NoError(t, err)
	require.Equal(t, "3.9.1", ver)
}

func placeExecutable(t *testing.T, name string, dirs ...string) {
	for _, createPath := range dirs {
		toCreate := filepath.Join(createPath, name)
		ostest.Create(t, toCreate)

		err := os.Chmod(toCreate, 0755)
		require.NoError(t, err)
	}
}

func mvnWithExt() string {
	if runtime.GOOS == "windows" {
		// For Windows, we want to test EXT resolution behavior
		return "mvn.cmd"
	} else {
		return "mvn"
	}
}

func mvnwWithExt() string {
	if runtime.GOOS == "windows" {
		// For Windows, we want to test EXT resolution behavior
		return "mvnw.cmd"
	} else {
		return "mvnw"
	}
}
