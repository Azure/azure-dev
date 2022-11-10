// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"regexp"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_toolInPath(t *testing.T) {
	t.Run("Missing", func(t *testing.T) {
		has, err := ToolInPath("somethingThatNeverExists")
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("Installed", func(t *testing.T) {
		// 'az' is a prerequisite to even develop in this package right now.
		has, err := ToolInPath("az")
		require.NoError(t, err)
		require.True(t, has)
	})
}

func Test_Unique(t *testing.T) {
	toolOne := &mockTool{
		name:             "Installed One",
		installUrl:       "https://example.com/tools/installed1",
		checkInstalledFn: func(_ context.Context) (bool, error) { return true, nil },
	}
	toolTwo := &mockTool{
		name:             "Installed Two",
		installUrl:       "https://example.com/tools/installed2",
		checkInstalledFn: func(_ context.Context) (bool, error) { return true, nil },
	}

	uniqueTools := Unique([]ExternalTool{toolOne, toolTwo, toolOne})
	assert.Equal(t, 2, len(uniqueTools))
	assert.Equal(t, toolOne, uniqueTools[0])
	assert.Equal(t, toolTwo, uniqueTools[1])
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

func TestExtractVersion(t *testing.T) {
	type args struct {
		cliOutput string
	}
	tests := []struct {
		name    string
		args    args
		want    semver.Version
		wantErr bool
	}{
		// Structured
		{"BetaWithBuild", args{"tool 18.1.2-beta+1234"}, semver.Version{Major: 18, Minor: 1, Patch: 2}, false},
		{"Beta", args{"tool 18.1.2-beta"}, semver.Version{Major: 18, Minor: 1, Patch: 2}, false},
		{"MajorMinorPatch", args{"tool 18.1.2"}, semver.Version{Major: 18, Minor: 1, Patch: 2}, false},
		{"MajorMinor", args{"tool 18.1"}, semver.Version{Major: 18, Minor: 1}, false},
		{"MajorMinorWithLetter", args{"18.1.b"}, semver.Version{Major: 18, Minor: 1}, false},
		{"Major", args{"tool 18"}, semver.Version{Major: 18}, false},
		{"MajorWithLetters", args{"18.a.b"}, semver.Version{Major: 18}, false},
		// Less structured output
		{"Prefixed", args{"tool v18.1.2, build 123"}, semver.Version{Major: 18, Minor: 1, Patch: 2}, false},
		{"Infixed", args{"tool v18.1.2sha123123"}, semver.Version{Major: 18, Minor: 1, Patch: 2}, false},
		// Failures
		{"Empty", args{""}, semver.Version{}, true},
		{"NoNumber", args{"tool"}, semver.Version{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractVersion(tt.args.cliOutput)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, got)
		})
	}
}
