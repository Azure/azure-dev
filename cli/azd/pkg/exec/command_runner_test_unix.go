// go:build unix

package exec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAbortSignalRetainsOutputError(t *testing.T) {
	runner := NewCommandRunner(nil)

	// Run a small shell script that prints to stdout, stderr and then aborts and ensure that the returned error
	// contains the output from both stdout and stderr.
	_, err := runner.Run(context.Background(), NewRunArgs("/bin/sh", "-c", "echo Hello ; >&2 echo World ; kill -s ABRT $$"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Hello")
	require.Contains(t, err.Error(), "World")
}
