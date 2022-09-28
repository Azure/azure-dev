package javac

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckInstalledVersion(t *testing.T) {
	javaHome := t.TempDir()
	javaHomeBin := filepath.Join(javaHome, "bin")
	require.NoError(t, os.Mkdir(javaHomeBin, 0755))

	ostest.CreateNoCleanup(t, filepath.Join(javaHomeBin, javacWithExt()))
	ostest.Setenv(t, "JAVA_HOME", javaHome)

	tests := []struct {
		name    string
		stdOut  string
		want    bool
		wantErr bool
	}{
		{name: "MetExact", stdOut: "javac 17.0.0.0", want: true},
		{name: "Met", stdOut: "javac 18.0.2.1", want: true},
		{name: "NotMet", stdOut: "javac 15.0.0.0", wantErr: true},
		{name: "InvalidSemVer", stdOut: "javac NoVer", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execMock := mockexec.NewMockCommandRunner().
				When(func(a azdexec.RunArgs, command string) bool { return true }).
				Respond(azdexec.NewRunResult(0, tt.stdOut, ""))

			cli := NewCli(execMock)
			ok, err := cli.CheckInstalled(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, ok)
		})
	}
}

func Test_getInstalledPath(t *testing.T) {
	jdkHome := t.TempDir()
	jdkHomeBin := filepath.Join(jdkHome, "bin")
	require.NoError(t, os.Mkdir(jdkHomeBin, 0755))

	javaHome := t.TempDir()
	javaHomeBin := filepath.Join(javaHome, "bin")
	require.NoError(t, os.Mkdir(javaHomeBin, 0755))

	origPath := os.Getenv("PATH")
	pathBin := t.TempDir()
	pathVal := fmt.Sprintf("%s%c%s", pathBin, os.PathListSeparator, origPath)
	ostest.Unsetenvs(t, []string{"JAVA_HOME", "PATH"})

	tests := []struct {
		name               string
		javacPaths         []string
		envVar             map[string]string
		testWindowsPathExt bool
		want               string
		wantErr            bool
	}{
		{
			name:       "JavaHome",
			javacPaths: []string{javaHomeBin},
			envVar:     map[string]string{"JAVA_HOME": javaHome},
			want:       filepath.Join(javaHomeBin, javacWithExt()),
			wantErr:    false,
		},
		{
			name:       "Path",
			javacPaths: []string{pathBin},
			envVar:     map[string]string{"PATH": pathVal},
			want:       filepath.Join(pathBin, javacWithExt()),
			wantErr:    false,
		},
		{
			name:       "SearchJavaHomeFirst",
			javacPaths: []string{javaHomeBin, pathBin},
			envVar:     map[string]string{"JAVA_HOME": javaHome, "PATH": pathVal},
			want:       filepath.Join(javaHomeBin, javacWithExt()),
			wantErr:    false,
		},
		{name: "InvalidJavaHome", envVar: map[string]string{"JAVA_HOME": javaHome}, wantErr: true},
		{name: "NotFound", envVar: map[string]string{"PATH": pathBin}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placeJavac(t, tt.javacPaths...)
			ostest.Setenvs(t, tt.envVar)

			actual, err := getInstalledPath()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, actual)
		})
	}
}

func placeJavac(t *testing.T, dirs ...string) {
	for _, createPath := range dirs {
		toCreate := filepath.Join(createPath, javacWithExt())
		ostest.Create(t, toCreate)
	}
}

func javacWithExt() string {
	if runtime.GOOS == "windows" {
		// For Windows, we want to test EXT resolution behavior
		return javac + ".exe"
	} else {
		return javac
	}
}
