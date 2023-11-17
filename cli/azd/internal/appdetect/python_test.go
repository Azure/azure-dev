package appdetect

import (
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
