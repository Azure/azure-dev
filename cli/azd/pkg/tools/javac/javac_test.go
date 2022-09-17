package javac

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_getInstalledPath(t *testing.T) {
	jdkHome := t.TempDir()
	jdkHomeBin := filepath.Join(jdkHome, "bin")
	//require.NoError(t, os.Mkdir(jdkHomeBin, 755))

	javaHome := t.TempDir()
	javaHomeBin := filepath.Join(javaHome, "bin")
	require.NoError(t, os.Mkdir(javaHomeBin, 755))

	path := t.TempDir()
	pathVal := fmt.Sprintf("%s%c%s", path, os.PathListSeparator, os.Getenv("PATH"))
	ostest.UnsetTempEnv(t, "JDK_HOME")
	ostest.UnsetTempEnv(t, "JAVA_HOME")
	ostest.UnsetTempEnv(t, "PATH")

	tests := []struct {
		name               string
		pathsPresent       []string
		envVar             map[string]string
		testWindowsPathExt bool
		want               string
		wantErr            bool
	}{
		{
			name:         "JdkHome",
			pathsPresent: []string{jdkHomeBin},
			envVar:       map[string]string{"JDK_HOME": jdkHome, "JAVA_HOME": ""},
			want:         filepath.Join(jdkHomeBin, "javac"),
			wantErr:      false,
		},
		{
			name:         "JavaHome",
			pathsPresent: []string{javaHomeBin},
			envVar:       map[string]string{"JAVA_HOME": javaHome, "JDK_HOME": ""},
			want:         filepath.Join(javaHomeBin, "javac"),
			wantErr:      false,
		},
		{
			name:         "Path",
			pathsPresent: []string{path},
			envVar:       map[string]string{"PATH": pathVal},
			want:         filepath.Join(path, "javac"),
			wantErr:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, createPath := range tt.pathsPresent {
				toCreate := filepath.Join(createPath, "javac")
				// For Windows, we want to test EXT resolution behavior
				if runtime.GOOS == "windows" {
					toCreate += ".exe"
				}
				f, err := os.Create(toCreate)
				require.NoError(t, err)
				defer f.Close()
			}

			ostest.SetTempEnvs(t, tt.envVar)

			actual, err := getInstalledPath()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				if runtime.GOOS == "windows" {
					assert.Equal(t, tt.want+".exe", actual)
				} else {
					assert.Equal(t, tt.want, actual)
				}
			}
		})
	}
}
