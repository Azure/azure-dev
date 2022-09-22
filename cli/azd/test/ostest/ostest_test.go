package ostest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCleanup(t *testing.T) {
	temp := TempDirWithDiagnostics(t)

	go func() {
		_, err := os.Create(filepath.Join(temp, ".locked"))
		require.NoError(t, err)
	}()

	time.Sleep(10 * time.Millisecond)
}
