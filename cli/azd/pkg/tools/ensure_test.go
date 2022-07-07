package tools

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_Unique(t *testing.T) {
	npmCli := NewNpmCli()
	pythonCli := NewPythonCli()

	uniqueTools := Unique([]ExternalTool{npmCli, pythonCli, npmCli})
	assert.Equal(t, 2, len(uniqueTools))
	assert.Equal(t, npmCli, uniqueTools[0])
	assert.Equal(t, pythonCli, uniqueTools[1])
}

func Test_EnsureInstalled(t *testing.T) {
	installedToolOne := &mockTool{
		name:             "Installed One",
		installUrl:       "https://example.com/tools/installed1",
		checkInstalledFn: func(_ context.Context) (bool, error) { return true, nil },
	}

	installedToolTwo := &mockTool{
		name:             "Installed Two",
		installUrl:       "https://example.com/tools/installed2",
		checkInstalledFn: func(_ context.Context) (bool, error) { return true, nil },
	}

	missingToolOne := &mockTool{
		name:             "Missing One",
		installUrl:       "https://example.com/tools/missing1",
		checkInstalledFn: func(_ context.Context) (bool, error) { return false, nil },
	}

	missingToolTwo := &mockTool{
		name:             "Missing Two",
		installUrl:       "https://example.com/tools/missing2",
		checkInstalledFn: func(_ context.Context) (bool, error) { return false, nil },
	}

	t.Run("HaveAll", func(t *testing.T) {
		err := EnsureInstalled(context.Background(), installedToolOne, installedToolTwo)
		assert.NoError(t, err)
	})

	t.Run("MissingOne", func(t *testing.T) {
		err := EnsureInstalled(context.Background(), installedToolOne, missingToolOne)
		assert.Error(t, err)
		assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta(missingToolOne.Name())), err.Error())
		assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta(missingToolOne.InstallUrl())), err.Error())
	})

	t.Run("MissingMany", func(t *testing.T) {
		err := EnsureInstalled(context.Background(), installedToolOne, missingToolOne, missingToolTwo)
		assert.Error(t, err)
		assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta(missingToolOne.Name())), err.Error())
		assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta(missingToolOne.InstallUrl())), err.Error())
		assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta(missingToolTwo.Name())), err.Error())
		assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta(missingToolTwo.InstallUrl())), err.Error())
	})
}

type mockTool struct {
	checkInstalledFn func(context.Context) (bool, error)
	installUrl       string
	name             string
}

var _ ExternalTool = &mockTool{}

func (m *mockTool) CheckInstalled(ctx context.Context) (bool, error) {
	return m.checkInstalledFn(ctx)
}

func (m *mockTool) InstallUrl() string {
	return m.installUrl
}

func (m *mockTool) Name() string {
	return m.name
}
