package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_EnsureInstalledOnlyOnce(t *testing.T) {
	ctx := context.Background()
	tool := TestTool{}

	_ = EnsureInstalled(ctx, &tool)
	_ = EnsureInstalled(ctx, &tool)

	require.Equal(t, tool.installChecks, 1)
}

type TestTool struct {
	installChecks int
}

func (t *TestTool) CheckInstalled(ctx context.Context) (bool, error) {
	t.installChecks++
	return true, nil
}

func (t *TestTool) InstallUrl() string {
	return "http://www.microsoft.com"
}

func (t *TestTool) Name() string {
	return "Test Tool"
}
