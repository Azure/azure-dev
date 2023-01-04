package javac

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckInstalledVersion(t *testing.T) {
	javaHome := t.TempDir()
	javaHomeBin := filepath.Join(javaHome, "bin")
	require.NoError(t, os.Mkdir(javaHomeBin, 0755))

	placeJavac(t, javaHomeBin)
	t.Setenv("JAVA_HOME", javaHome)

	tests := []struct {
		name    string
		stdOut  string
		want    bool
		wantErr bool
	}{
		{name: "MetExact", stdOut: "javac 17.0.0.0", want: true},
		{name: "Met", stdOut: "javac 18.0.2.1", want: true},
		{name: "MetMajorOnly", stdOut: "javac 19", want: true},
		{name: "NotMet", stdOut: "javac 15.0.0.0", wantErr: true},
		{name: "NotMetMajorOnly", stdOut: "javac 11", wantErr: true},
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
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.want, ok)
		})
	}
}

func TestCheckInstalled_OlderJavaVersion(t *testing.T) {
	javaHome := t.TempDir()
	javaHomeBin := filepath.Join(javaHome, "bin")
	require.NoError(t, os.Mkdir(javaHomeBin, 0755))

	placeJavac(t, javaHomeBin)
	t.Setenv("JAVA_HOME", javaHome)

	// error when --version
	execMock := mockexec.NewMockCommandRunner().
		When(func(a azdexec.RunArgs, command string) bool { return a.Args[0] == "--version" }).
		RespondFn(func(args azdexec.RunArgs) (azdexec.RunResult, error) {
			return azdexec.NewRunResult(2, "", ""), errors.New("--version not recognized")
		})

	// non-zero exit code on -version
	execMock = execMock.
		When(func(a azdexec.RunArgs, command string) bool { return a.Args[0] == "-version" }).
		Respond(azdexec.NewRunResult(0, "", "javac 1.8_353"))

	cli := NewCli(execMock)
	ok, err := cli.CheckInstalled(context.Background())

	assert.False(t, ok)
	assert.ErrorContains(t, err, "need at least version")
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

		err := os.Chmod(toCreate, 0755)
		require.NoError(t, err)
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
