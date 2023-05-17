package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_EnsureInstalledOnlyOnceWhenCached(t *testing.T) {
	ctx := WithInstalledCheckCache(context.Background())
	tool := TestTool{}

	_ = EnsureInstalled(ctx, &tool)
	_ = EnsureInstalled(ctx, &tool)

	require.Equal(t, tool.installChecks, 1)
}

type TestTool struct {
	installChecks int
}

func (t *TestTool) CheckInstalled(ctx context.Context) error {
	t.installChecks++
	return nil
}

func (t *TestTool) InstallUrl() string {
	return "http://www.microsoft.com"
}

func (t *TestTool) Name() string {
	return "Test Tool"
}
