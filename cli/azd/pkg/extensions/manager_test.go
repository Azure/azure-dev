// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_ValidateChecksum_Success_SHA256(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Compute the expected checksum
	hash := sha256.Sum256(content)
	expectedChecksum := hex.EncodeToString(hash[:])

	// Create the checksum struct
	checksum := ExtensionChecksum{
		Algorithm: "sha256",
		Value:     expectedChecksum,
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.NoError(t, err)
}

func Test_ValidateChecksum_Success_SHA512(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Compute the expected checksum
	hash := sha512.Sum512(content)
	expectedChecksum := hex.EncodeToString(hash[:])

	// Create the checksum struct
	checksum := ExtensionChecksum{
		Algorithm: "sha512",
		Value:     expectedChecksum,
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.NoError(t, err)
}

func Test_ValidateChecksum_Failure_InvalidAlgorithm(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with an invalid algorithm
	checksum := ExtensionChecksum{
		Algorithm: "invalid",
		Value:     "dummychecksum",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported checksum algorithm")
}

func Test_ValidateChecksum_Failure_ChecksumMismatch(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with an incorrect checksum value
	checksum := ExtensionChecksum{
		Algorithm: "sha256",
		Value:     "incorrectchecksum",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checksum mismatch")
}

func Test_ValidateChecksum_Failure_InvalidChecksumData(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with missing algorithm and value
	checksum := ExtensionChecksum{
		Algorithm: "",
		Value:     "",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)

	// Empty checksum skips verification
	require.NoError(t, err)
}

func Test_List_Install_Uninstall_Flow(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// List installed extensions (expect 0)
	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, 0, len(installed))

	// List extensions from the registry (expect at least 1)
	extensions, err := manager.FindExtensions(*mockContext.Context, nil)
	require.NoError(t, err)
	require.NotNil(t, extensions)
	require.Greater(t, len(extensions), 0)

	// Install the first extension
	extensionVersion, err := manager.Install(*mockContext.Context, extensions[0], "")
	require.NoError(t, err)
	require.NotNil(t, extensionVersion)

	// List installed extensions (expect 1)
	installed, err = manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Greater(t, len(installed), 0)

	// Uninstall the first extension
	err = manager.Uninstall(t.Context(), extensions[0].Id)
	require.NoError(t, err)

	// List installed extensions (expect 0)
	installed, err = manager.ListInstalled()
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, 0, len(installed))
}

func Test_ReloadUserConfig_PicksUpOutOfBandChanges(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Mutate the user configuration out-of-band, after the manager has cached its snapshot.
	err = sourceManager.Add(*mockContext.Context, "my-bundle", &SourceConfig{
		Type:     SourceKindBundle,
		Location: "/tmp/bundle",
	})
	require.NoError(t, err)

	sourcePath := fmt.Sprintf("%s.%s", baseConfigKey, "my-bundle")

	// After reloading, the cached snapshot reflects the out-of-band change, so a
	// subsequent install save will not clobber the newly added source.
	require.NoError(t, manager.ReloadUserConfig())
	_, ok := manager.userConfig.Get(sourcePath)
	require.True(t, ok)
}

func Test_Install_With_SemverConstraints(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Generate a list of tests cases to validate the semver constraints of the Install function.
	testCases := []struct {
		Constraint string
		Expected   string
	}{
		{
			Constraint: "latest",
			Expected:   "3.1.0",
		},
		{
			Constraint: "*",
			Expected:   "3.1.0",
		},
		{
			Constraint: "2.x",
			Expected:   "2.1.1",
		},
		{
			Constraint: "1.0.0",
			Expected:   "1.0.0",
		},
		{
			Constraint: "=2.1.1",
			Expected:   "2.1.1",
		},
		{
			Constraint: ">=1.0.0",
			Expected:   "3.1.0",
		},
		{
			Constraint: ">=1.0.0 <2.0.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: ">=1.0.0 <1.2.0",
			Expected:   "1.1.0",
		},
		{
			Constraint: ">=1.0.0 <1.1.0",
			Expected:   "1.0.0",
		},
		{
			Constraint: ">=1.0.0 <2.0.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: ">=1.0.0 <1.0.0 || >=2.0.0 <3.0.0",
			Expected:   "2.1.1",
		},
		{
			Constraint: "~2.1.0",
			Expected:   "2.1.1",
		},
		{
			Constraint: "^1.0.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: "^1.1.0",
			Expected:   "1.3.0",
		},
		{
			Constraint: "invalid",
			Expected:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Constraint, func(t *testing.T) {
			// Find the extension first
			extensions, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.extension"})
			require.NoError(t, err)
			require.Len(t, extensions, 1)

			extensionVersion, err := manager.Install(*mockContext.Context, extensions[0], tc.Constraint)
			if tc.Expected == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, extensionVersion)
				require.Equal(t, tc.Expected, extensionVersion.Version)

				err = manager.Uninstall(t.Context(), "test.extension")
				require.NoError(t, err)
			}
		})
	}
}

func Test_MatchesVersionConstraint(t *testing.T) {
	testCases := []struct {
		Name      string
		Expr      string
		Candidate string
		Want      bool
	}{
		{Name: "empty matches any", Expr: "", Candidate: "0.1.0", Want: true},
		{Name: "latest matches any", Expr: "latest", Candidate: "0.1.0", Want: true},
		{Name: "Latest case-insensitive", Expr: "Latest", Candidate: "9.9.9-rc1", Want: true},
		{Name: "exact pin match", Expr: "0.1.31-preview", Candidate: "0.1.31-preview", Want: true},
		{Name: "exact pin miss", Expr: "0.1.31-preview", Candidate: "0.1.32-preview", Want: false},
		{Name: "ge matches base", Expr: ">=0.1.0", Candidate: "0.1.0", Want: true},
		// Pre-release versions are only matched by constraints that explicitly include a
		// pre-release tag on the lower bound (Masterminds/semver semantics).
		{Name: "ge does not match pre-release", Expr: ">=0.1.0", Candidate: "0.1.31-preview", Want: false},
		{
			Name:      "ge with preview lower bound matches pre-release",
			Expr:      ">=0.1.0-0",
			Candidate: "0.1.31-preview",
			Want:      true,
		},
		{Name: "ge matches higher major", Expr: ">=0.1.0", Candidate: "1.0.0", Want: true},
		{Name: "range matches in-range", Expr: ">=1.0.0,<2.0.0", Candidate: "1.5.0", Want: true},
		{Name: "range rejects below", Expr: ">=1.0.0,<2.0.0", Candidate: "0.9.0", Want: false},
		{Name: "range rejects upper bound", Expr: ">=1.0.0,<2.0.0", Candidate: "2.0.0", Want: false},
		{Name: "caret matches pre-release within range", Expr: "^0.1.0-0", Candidate: "0.1.31-preview", Want: true},
		{Name: "caret rejects next minor", Expr: "^0.1", Candidate: "0.2.0", Want: false},
		{Name: "tilde matches pre-release within range", Expr: "~0.1.0-preview", Candidate: "0.1.31-preview", Want: true},
		{Name: "tilde rejects next minor", Expr: "~0.1.0-preview", Candidate: "0.2.0", Want: false},
		{Name: "unparseable expr falls back to exact match", Expr: "dev", Candidate: "dev", Want: true},
		{
			Name:      "unparseable expr fallback case-insensitive",
			Expr:      "Nightly",
			Candidate: "nightly",
			Want:      true,
		},
		{Name: "unparseable expr no match", Expr: "dev", Candidate: "0.1.0", Want: false},
		{
			Name:      "non-semver candidate falls back to exact",
			Expr:      ">=0.1.0",
			Candidate: "nightly",
			Want:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			got := matchesVersionConstraint(tc.Expr, tc.Candidate)
			require.Equal(t, tc.Want, got)
		})
	}
}

func Test_CreateExtensionFilter_VersionConstraints(t *testing.T) {
	ext := &ExtensionMetadata{
		Id: "test.constraints",
		Versions: []ExtensionVersion{
			{Version: "0.1.0"},
			{Version: "0.1.31-preview"},
			{Version: "1.0.0"},
			{Version: "1.5.0"},
			{Version: "2.0.0"},
		},
	}

	testCases := []struct {
		Name    string
		Version string
		Match   bool
	}{
		{Name: "empty matches", Version: "", Match: true},
		{Name: "latest matches", Version: "latest", Match: true},
		{Name: "exact pin", Version: "0.1.31-preview", Match: true},
		{Name: "ge across versions", Version: ">=0.1.0", Match: true},
		{Name: "range matches 1.5.0", Version: ">=1.0.0,<2.0.0", Match: true},
		{Name: "caret 0.1 with preview lower bound", Version: "^0.1.0-0", Match: true},
		{Name: "tilde preview", Version: "~0.1.0-preview", Match: true},
		{Name: "unparseable matches none", Version: "dev", Match: false},
		{Name: "constraint matches nothing", Version: ">=99.0.0", Match: false},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			filter := createExtensionFilter(&FilterOptions{Version: tc.Version})
			require.Equal(t, tc.Match, filter(ext))
		})
	}
}

func Test_Install_PackDependency_SemverConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	packRegistry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.pack",
				Namespace:   "test",
				DisplayName: "Test Pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.dependency", Version: ">=0.1.0-0"},
						},
					},
				},
			},
			{
				Id:          "test.dependency",
				Namespace:   "test",
				DisplayName: "Test Dependency",
				Versions: []ExtensionVersion{
					{Version: "0.1.0", Artifacts: sampleArtifacts},
					{Version: "0.1.15-preview", Artifacts: sampleArtifacts},
					{Version: "0.1.31-preview", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, packRegistry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Len(t, packs, 1)

	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	installed, err := manager.GetInstalled(FilterOptions{Id: "test.dependency"})
	require.NoError(t, err)
	require.NotNil(t, installed)
	require.Equal(t, "0.1.31-preview", installed.Version)
}

func Test_Install_PackDependency_InstalledDependencyMustSatisfyConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	children, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, children[0], "1.0.0")
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "installed dependency test.child version 1.0.0 does not satisfy constraint")
}

func Test_Install_PackDependency_UsesCompatibleDependencyVersion(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", RequiredAzdVersion: ">=99.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	azdVersion, err := semver.NewVersion("1.0.0")
	require.NoError(t, err)
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.InstallWithOptions(*mockContext.Context, packs[0], InstallOptions{
		AzdVersion: azdVersion,
	})
	require.NoError(t, err)

	installed, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", installed.Version)
}

// Test_Install_SkipDependencies verifies that InstallOptions.SkipDependencies
// installs only the target extension and does not pull in its declared
// dependencies.
func Test_Install_SkipDependencies(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version:      "1.0.0",
						Artifacts:    sampleArtifacts,
						Dependencies: []ExtensionDependency{{Id: "test.child", Version: ">=1.0.0"}},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.InstallWithOptions(*mockContext.Context, packs[0], InstallOptions{
		SkipDependencies: true,
	})
	require.NoError(t, err)

	// The parent is installed but the dependency is not.
	installedPack, err := manager.GetInstalled(FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.NotNil(t, installedPack)

	_, err = manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.ErrorIs(t, err, ErrInstalledExtensionNotFound,
		"dependency must not be installed when SkipDependencies is set")
}

// Test_Install_SkipDependencies_BypassesConstraintCheck verifies that
// SkipDependencies installs the parent even when an already-installed dependency
// does not satisfy the parent's constraint. This is the scenario that breaks a
// coordinated multi-extension bump: a meta-package still pins an old constraint
// against a newer, already-installed child.
func Test_Install_SkipDependencies_BypassesConstraintCheck(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version:      "1.0.0",
						Artifacts:    sampleArtifacts,
						Dependencies: []ExtensionDependency{{Id: "test.child", Version: ">=2.0.0"}},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install an older child that does NOT satisfy the pack's >=2.0.0 constraint.
	children, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, children[0], "1.0.0")
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)

	// Without SkipDependencies this fails the installed-dependency constraint check.
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.Error(t, err)

	// With SkipDependencies the parent installs regardless of the stale constraint.
	_, err = manager.InstallWithOptions(*mockContext.Context, packs[0], InstallOptions{
		SkipDependencies: true,
	})
	require.NoError(t, err)
}

// Test_Upgrade_SkipDependencies verifies that UpgradeOptions.SkipDependencies is
// honored on the reinstall/upgrade path: a parent with an unresolvable (missing)
// dependency upgrades successfully without attempting to install the dependency
// and without leaving the parent uninstalled.
func Test_Upgrade_SkipDependencies(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.parent",
				Versions: []ExtensionVersion{
					{
						Version:      "1.0.0",
						Artifacts:    sampleArtifacts,
						Dependencies: []ExtensionDependency{{Id: "test.missing", Version: ">=1.0.0"}},
					},
					{
						Version:      "2.0.0",
						Artifacts:    sampleArtifacts,
						Dependencies: []ExtensionDependency{{Id: "test.missing", Version: ">=1.0.0"}},
					},
				},
			},
			// Note: test.missing is intentionally absent from the registry.
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	parents, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.parent"})
	require.NoError(t, err)

	// Fresh install at 1.0.0 with dependencies skipped.
	_, err = manager.InstallWithOptions(*mockContext.Context, parents[0], InstallOptions{
		VersionPreference: "1.0.0",
		SkipDependencies:  true,
	})
	require.NoError(t, err)

	// Upgrade to 2.0.0 with dependencies skipped: must not try to install the
	// missing dependency, must not error, and must not leave the parent removed.
	_, _, err = manager.Upgrade(*mockContext.Context, parents[0], UpgradeOptions{
		VersionPreference: "2.0.0",
		SkipDependencies:  true,
	})
	require.NoError(t, err)

	installedParent, err := manager.GetInstalled(FilterOptions{Id: "test.parent"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", installedParent.Version)

	_, err = manager.GetInstalled(FilterOptions{Id: "test.missing"})
	require.ErrorIs(t, err, ErrInstalledExtensionNotFound,
		"missing dependency must not be installed when SkipDependencies is set on upgrade")
}

func Test_DownloadArtifact_Remote(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Mock the HTTP client to simulate a remote download
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == "https://example.com/artifact.zip"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("artifact content"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, "https://example.com/artifact.zip")
	require.NoError(t, err)
	require.FileExists(t, tempFilePath)

	// Clean up the temp file
	defer os.Remove(tempFilePath)
}

func Test_DownloadArtifact_Local(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Create a temporary file to simulate a local artifact
	content := []byte("local artifact content")
	tempFile, err := os.CreateTemp(t.TempDir(), "artifact")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, tempFile.Name())
	require.NoError(t, err)
	require.FileExists(t, tempFilePath)

	// Clean up the temp file
	defer os.Remove(tempFilePath)
}

func Test_DownloadArtifact_Local_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Provide an invalid local file path
	invalidFilePath := "non-existent-file.txt"

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, invalidFilePath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "file does not exist at path")
	require.Empty(t, tempFilePath)
}

func Test_DownloadArtifact_Remote_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Mock the HTTP client to simulate a failed remote download
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == "https://example.com/invalid-artifact.zip"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, http.StatusNotFound)
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	tempFilePath, err := manager.downloadArtifact(*mockContext.Context, "https://example.com/invalid-artifact.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to download file")
	require.Empty(t, tempFilePath)
}

func Test_ValidateChecksum_Error_InvalidFile(t *testing.T) {
	// Create a non-existent file path
	nonExistentFilePath := "non-existent-file.txt"

	// Create the checksum struct
	checksum := ExtensionChecksum{
		Algorithm: "sha256",
		Value:     "dummychecksum",
	}

	// Validate the checksum
	err := validateChecksum(nonExistentFilePath, checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to open file for checksum validation")
}

func Test_ValidateChecksum_Error_UnsupportedAlgorithm(t *testing.T) {
	// Create a temporary file with known content
	content := []byte("test data")
	tempFile, err := os.CreateTemp(t.TempDir(), "testfile")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write(content)
	require.NoError(t, err)
	tempFile.Close()

	// Create the checksum struct with an unsupported algorithm
	checksum := ExtensionChecksum{
		Algorithm: "unsupported",
		Value:     "dummychecksum",
	}

	// Validate the checksum
	err = validateChecksum(tempFile.Name(), checksum)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported checksum algorithm")
}

func createRegistryMocks(mockContext *mocks.MockContext) {
	// Create a mock source
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, testRegistry)
	})

	// Return mock file for any extension artifact download
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})
}

var sampleArtifacts = map[string]ExtensionArtifact{
	"darwin": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-darwin-amd64",
		AdditionalMetadata: map[string]any{
			"entryPoint": "azd-ext-test-darwin-amd64",
		},
	},
	"windows": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-windows-amd64.exe",
		AdditionalMetadata: map[string]any{
			"entryPoint": "azd-ext-test-windows-amd64.exe",
		},
	},
	"linux": {
		//nolint:lll
		URL: "https://aka.ms/azd/extensions/registry/test.extension/azd-ext-test-linux-amd64",
		AdditionalMetadata: map[string]any{
			"entryPoint": "azd-ext-test-linux-amd64",
		},
	},
}

var testRegistry = Registry{
	Extensions: []*ExtensionMetadata{
		{
			Id:          "test.extension",
			Namespace:   "test",
			DisplayName: "Test Extension",
			Description: "Test extension description",
			Tags:        []string{"test"},
			Versions: []ExtensionVersion{
				{
					Version:   "1.0.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "1.1.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "1.2.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "1.3.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "2.0.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "2.1.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "2.1.1",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "3.0.0",
					Artifacts: sampleArtifacts,
				},
				{
					Version:   "3.1.0",
					Artifacts: sampleArtifacts,
				},
			},
		},
		{
			Id:          "test.mcp.extension",
			Namespace:   "test.mcp",
			DisplayName: "Test MCP Extension",
			Description: "Test extension with MCP configuration",
			Tags:        []string{"test", "mcp"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{McpServerCapability},
					McpConfig: &McpConfig{
						Server: McpServerConfig{
							Args: []string{"custom", "mcp", "start"},
							Env:  []string{"CUSTOM_VAR=test", "DEBUG=${HOME}/debug"},
						},
					},
				},
			},
		},
		{
			Id:          "azure.containerapp",
			Namespace:   "azure",
			DisplayName: "Azure Container Apps Extension",
			Description: "Extension for deploying to Azure Container Apps",
			Tags:        []string{"azure", "containerapp", "service"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{ServiceTargetProviderCapability},
					Providers: []Provider{
						{
							Name:        "containerapp",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Azure Container Apps",
						},
					},
				},
			},
		},
		{
			Id:          "kubernetes.deploy",
			Namespace:   "kubernetes",
			DisplayName: "Kubernetes Deployment Extension",
			Description: "Extension with Kubernetes deployment and MCP capabilities",
			Tags:        []string{"kubernetes", "multi", "service", "mcp"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{ServiceTargetProviderCapability, McpServerCapability},
					Providers: []Provider{
						{
							Name:        "kubernetes",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Kubernetes",
						},
					},
				},
			},
		},
		{
			Id:          "foundry.multi.target",
			Namespace:   "foundry",
			DisplayName: "Multi-Target Foundry Extension",
			Description: "Extension supporting multiple deployment targets",
			Tags:        []string{"foundry", "multi", "providers"},
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Artifacts:    sampleArtifacts,
					Capabilities: []CapabilityType{ServiceTargetProviderCapability},
					Providers: []Provider{
						{
							Name:        "azure.ai.agents",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Microsoft Foundry hosted agents",
						},
						{
							Name:        "containerapp",
							Type:        ServiceTargetProviderType,
							Description: "Deploys to Azure Container Apps",
						},
					},
				},
			},
		},
	},
}

func Test_FindArtifactForCurrentOS_ErrorMessage_Format(t *testing.T) {
	// Create a version with artifacts that don't match current OS/architecture
	// For this test, we'll use artificial platform names that definitely won't match
	version := &ExtensionVersion{
		Artifacts: map[string]ExtensionArtifact{
			"fakeos": {
				URL: "https://example.com/fakeos-binary",
			},
			"anotherfakeos/fakearch": {
				URL: "https://example.com/anotherfakeos-binary",
			},
		},
	}

	artifact, err := findArtifactForCurrentOS(version)

	require.Error(t, err)
	require.Nil(t, artifact)
	require.Contains(t, err.Error(), "no artifact available for platform:")
}

func Test_FindExtensions_MultipleMatches_ErrorHandling(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)

	// Mock two different registries that both contain the same extension ID
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		// Return a registry with an extension
		registry := Registry{
			Extensions: []*ExtensionMetadata{
				{
					Id:          "duplicate.extension",
					Namespace:   "duplicate",
					DisplayName: "Duplicate Extension from Registry",
					Description: "Extension from first source",
					Source:      "default", // This will be overwritten by the source
					Tags:        []string{"test"},
					Versions: []ExtensionVersion{
						{
							Version:   "1.0.0",
							Artifacts: sampleArtifacts,
						},
					},
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})

	// Create a manager and mock two sources
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Create mock sources that will return the same extension
	mockSource1 := &mockSource{
		name: "source1",
		extensions: []*ExtensionMetadata{
			{
				Id:          "duplicate.extension",
				Namespace:   "duplicate",
				DisplayName: "Extension from Source 1",
				Source:      "source1",
			},
		},
	}

	mockSource2 := &mockSource{
		name: "source2",
		extensions: []*ExtensionMetadata{
			{
				Id:          "duplicate.extension",
				Namespace:   "duplicate",
				DisplayName: "Extension from Source 2",
				Source:      "source2",
			},
		},
	}

	// Override the sources with our mocks
	manager.sources = []Source{mockSource1, mockSource2}

	// Try to find the extension - should return multiple matches
	extensions, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "duplicate.extension"})

	// Verify we got multiple matches (this is expected behavior for FindExtensions)
	require.NoError(t, err)
	require.Len(t, extensions, 2)

	// Verify both sources are represented
	sourceNames := make(map[string]bool)
	for _, ext := range extensions {
		sourceNames[ext.Source] = true
	}
	require.True(t, sourceNames["source1"])
	require.True(t, sourceNames["source2"])
}

func Test_FindExtensions_SourceConfigDirectSource(t *testing.T) {
	t.Parallel()

	registryPath := writeExtensionRegistryFile(t, Registry{
		SchemaVersion: CurrentRegistrySchemaVersion,
		Extensions: []*ExtensionMetadata{
			{
				Id:          "direct.extension",
				DisplayName: "Direct Extension",
				Versions: []ExtensionVersion{
					{Version: "1.0.0"},
				},
			},
		},
	})

	manager := newTestManager(t)

	extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
		Id:     "direct.extension",
		Source: "ignored-source-filter",
		SourceConfig: &SourceConfig{
			Name:     "direct",
			Type:     SourceKindFile,
			Location: registryPath,
		},
	})
	require.NoError(t, err)
	require.Len(t, extensions, 1)
	require.Equal(t, "direct.extension", extensions[0].Id)
	require.Equal(t, "direct", extensions[0].Source)
}

func Test_FindExtensions_SourceConfigMissingFileReturnsError(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)

	_, err := manager.FindExtensions(t.Context(), &FilterOptions{
		SourceConfig: &SourceConfig{
			Name:     "missing",
			Type:     SourceKindFile,
			Location: filepath.Join(t.TempDir(), "missing-registry.json"),
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed initializing extension source")
}

func Test_FindExtensions_SourceConfigUnsupportedSchemaReturnsSuggestion(t *testing.T) {
	t.Parallel()

	registryPath := writeExtensionRegistryFile(t, Registry{
		SchemaVersion: "2.0",
		Extensions:    []*ExtensionMetadata{},
	})

	manager := newTestManager(t)

	_, err := manager.FindExtensions(t.Context(), &FilterOptions{
		SourceConfig: &SourceConfig{
			Name:     "future",
			Type:     SourceKindFile,
			Location: registryPath,
		},
	})
	require.Error(t, err)
	require.ErrorAs(t, err, new(*ErrUnsupportedRegistrySchema))
	require.ErrorAs(t, err, new(*errorhandler.ErrorWithSuggestion))
}

func Test_UpdateInstalled_UpdatesConfigAndInvalidatesCache(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"test.extension": {
			Id:      "test.extension",
			Version: "1.0.0",
		},
	}))

	manager.installed = map[string]*Extension{
		"test.extension": {
			Id:      "test.extension",
			Version: "1.0.0",
		},
	}

	err := manager.UpdateInstalled(&Extension{
		Id:      "test.extension",
		Version: "2.0.0",
	})
	require.NoError(t, err)
	require.Nil(t, manager.installed)

	updated, err := manager.GetInstalled(FilterOptions{Id: "test.extension"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", updated.Version)
}

func Test_UpdateInstalled_MissingExtension(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)

	err := manager.UpdateInstalled(&Extension{Id: "missing"})
	require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
}

func Test_InvalidateSourceCache(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	manager.sources = []Source{&mockSource{name: "cached"}}

	manager.InvalidateSourceCache()

	require.Nil(t, manager.sources)
}

func Test_HasMetadataCapability(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"metadata.extension": {
			Id:           "metadata.extension",
			Capabilities: []CapabilityType{MetadataCapability},
		},
		"plain.extension": {
			Id: "plain.extension",
		},
	}))

	require.True(t, manager.HasMetadataCapability("metadata.extension"))
	require.False(t, manager.HasMetadataCapability("plain.extension"))
	require.False(t, manager.HasMetadataCapability("missing.extension"))
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	mockContext := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	return manager
}

func writeExtensionRegistryFile(t *testing.T, registry Registry) string {
	t.Helper()

	data, err := json.Marshal(registry)
	require.NoError(t, err)

	registryPath := filepath.Join(t.TempDir(), "registry.json")
	require.NoError(t, os.WriteFile(registryPath, data, 0600))

	return registryPath
}

// mockSource is a test implementation of the Source interface
type mockSource struct {
	name       string
	extensions []*ExtensionMetadata
}

func (m *mockSource) Name() string {
	return m.name
}

func (m *mockSource) ListExtensions(ctx context.Context) ([]*ExtensionMetadata, error) {
	return m.extensions, nil
}

func (m *mockSource) GetExtension(ctx context.Context, extensionId string) (*ExtensionMetadata, error) {
	for _, ext := range m.extensions {
		if ext.Id == extensionId {
			return ext, nil
		}
	}
	return nil, nil
}

func Test_Install_WithMcpConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Use the existing registry mock setup
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install extension with MCP configuration
	extensions, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.mcp.extension"})
	require.NoError(t, err)
	require.Len(t, extensions, 1)

	extensionVersion, err := manager.Install(*mockContext.Context, extensions[0], "")
	require.NoError(t, err)
	require.NotNil(t, extensionVersion)

	// Verify the extension was installed
	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.Equal(t, 1, len(installed))

	// Get the installed extension
	installedExtension, exists := installed["test.mcp.extension"]
	require.True(t, exists)
	require.NotNil(t, installedExtension)

	// Verify McpConfig was preserved during installation
	require.NotNil(t, installedExtension.McpConfig, "McpConfig should be preserved during installation")
	require.NotNil(t, installedExtension.McpConfig.Server, "McpServerConfig should be preserved")
	require.Equal(t, []string{"custom", "mcp", "start"}, installedExtension.McpConfig.Server.Args)
	require.Equal(t, []string{"CUSTOM_VAR=test", "DEBUG=${HOME}/debug"}, installedExtension.McpConfig.Server.Env)

	// Verify the extension has MCP server capability
	require.True(t, installedExtension.HasCapability(McpServerCapability))
}

// Helper function to convert extension slice to ID set
func extensionIdsToSet(extensions []*ExtensionMetadata) map[string]bool {
	ids := make(map[string]bool)
	for _, ext := range extensions {
		ids[ext.Id] = true
	}
	return ids
}

// Helper function to assert extension IDs match expectations
func assertExtensionIds(t *testing.T, extensions []*ExtensionMetadata, expectedIds []string, unexpectedIds []string) {
	t.Helper()
	ids := extensionIdsToSet(extensions)

	for _, expectedId := range expectedIds {
		require.True(t, ids[expectedId], "Expected to find extension: %s", expectedId)
	}

	for _, unexpectedId := range unexpectedIds {
		require.False(t, ids[unexpectedId], "Expected NOT to find extension: %s", unexpectedId)
	}
}

// Test_FilterExtensions_ByCapabilityAndProvider tests the capability and provider filtering functionality
func Test_FilterExtensions_ByCapabilityAndProvider(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	t.Run("filter by service-target-provider capability", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
		})
		require.NoError(t, err)
		require.Len(t, extensions, 3, "Should find 3 extensions with service-target-provider capability")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "kubernetes.deploy", "foundry.multi.target"},
			[]string{"test.mcp.extension"})
	})

	t.Run("filter by MCP capability", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: McpServerCapability,
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find 2 extensions with MCP capability")

		assertExtensionIds(t, extensions,
			[]string{"test.mcp.extension", "kubernetes.deploy"},
			[]string{"azure.containerapp", "foundry.multi.target"})
	})

	t.Run("find extension with containerapp provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find exactly 2 extensions with containerapp provider")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "foundry.multi.target"},
			[]string{"test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find extension with kubernetes provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "kubernetes",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension with kubernetes provider")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("find extension with azure.ai.agents provider", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "azure.ai.agents",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension with azure.ai.agents provider")

		assertExtensionIds(t, extensions,
			[]string{"foundry.multi.target"},
			[]string{"azure.containerapp", "test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find service target extension for containerapp", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
			Provider:   "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find exactly 2 extensions")

		assertExtensionIds(t, extensions,
			[]string{"azure.containerapp", "foundry.multi.target"},
			[]string{"test.mcp.extension", "kubernetes.deploy"})
	})

	t.Run("find service target extension for kubernetes", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
			Provider:   "kubernetes",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find exactly 1 extension")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("case-insensitive provider matching", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "KUBERNETES",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 1, "Should find extension with case-insensitive provider matching")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy"},
			[]string{"azure.containerapp", "test.mcp.extension", "foundry.multi.target"})
	})

	t.Run("filter with no matches", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Provider: "nonexistent-provider",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 0, "Should find no extensions with nonexistent provider")
	})

	t.Run("combine capability and tag filters", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: ServiceTargetProviderCapability,
			Tags:       []string{"multi"},
		})
		require.NoError(t, err)
		require.Len(t, extensions, 2, "Should find extensions with both capability and tag filters")

		assertExtensionIds(t, extensions,
			[]string{"kubernetes.deploy", "foundry.multi.target"},
			[]string{"azure.containerapp", "test.mcp.extension"})
	})

	t.Run("invalid capability and provider combination", func(t *testing.T) {
		extensions, err := manager.FindExtensions(t.Context(), &FilterOptions{
			Capability: McpServerCapability,
			Provider:   "containerapp",
		})
		require.NoError(t, err)
		require.Len(t, extensions, 0, "Should find no extensions with MCP capability AND containerapp provider")
	})
}

func Test_FetchAndCacheMetadata(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	createRegistryMocks(mockContext)

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)

	// Create a temporary directory for test extensions
	tempDir := t.TempDir()
	extDir := filepath.Join(tempDir, "extensions", "test.metadata.extension")
	err := os.MkdirAll(extDir, os.ModePerm)
	require.NoError(t, err)

	// Create a dummy extension binary
	extBinary := filepath.Join(extDir, "test-ext.exe")
	err = os.WriteFile(extBinary, []byte("fake binary"), 0600)
	require.NoError(t, err)

	// Get relative path from temp dir
	relPath, err := filepath.Rel(tempDir, extBinary)
	require.NoError(t, err)

	// Override user config directory for this test
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// Create a mock extension that supports metadata capability
	extension := &Extension{
		Id:           "test.metadata.extension",
		Namespace:    "test",
		DisplayName:  "Test Metadata Extension",
		Version:      "1.0.0",
		Path:         relPath,
		Capabilities: []CapabilityType{MetadataCapability},
	}

	// Mock the runner to return metadata JSON
	mockMetadata := ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            "test.metadata.extension",
		Commands: []Command{
			{
				Name:  []string{"test"},
				Short: "Test command",
				Usage: "azd test",
			},
		},
	}

	metadataJSON, err := json.Marshal(mockMetadata)
	require.NoError(t, err)

	// Mock CommandRunner to return metadata JSON
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "metadata")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout:   string(metadataJSON),
			ExitCode: 0,
		}, nil
	})

	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	t.Run("fetch metadata for extension with metadata capability", func(t *testing.T) {
		// Install extension first (simulate by adding to config)
		extensions, err := manager.ListInstalled()
		require.NoError(t, err)
		extensions[extension.Id] = extension
		err = manager.userConfig.Set(installedConfigKey, extensions)
		require.NoError(t, err)

		// Fetch and cache metadata
		err = manager.fetchAndCacheMetadata(*mockContext.Context, extension)
		require.NoError(t, err)

		// Verify metadata exists
		exists := manager.MetadataExists(extension.Id)
		require.True(t, exists, "Metadata should exist after fetch")

		// Load and verify metadata
		loadedMetadata, err := manager.LoadMetadata(extension.Id)
		require.NoError(t, err)
		require.NotNil(t, loadedMetadata)
		require.Equal(t, mockMetadata.ID, loadedMetadata.ID)
		require.Len(t, loadedMetadata.Commands, 1)
		require.Equal(t, "test", loadedMetadata.Commands[0].Name[0])
	})

	t.Run("caller should check capability before calling fetchAndCacheMetadata", func(t *testing.T) {
		extensionNoMetadata := &Extension{
			Id:           "test.no.metadata",
			Namespace:    "test",
			DisplayName:  "Test No Metadata Extension",
			Version:      "1.0.0",
			Path:         "extensions/test.no.metadata/test-ext.exe",
			Capabilities: []CapabilityType{}, // No metadata capability
		}

		// Caller should check capability before calling
		hasCapability := extensionNoMetadata.HasCapability(MetadataCapability)
		require.False(t, hasCapability, "Extension should not have metadata capability")

		// Metadata should not exist
		exists := manager.MetadataExists(extensionNoMetadata.Id)
		require.False(t, exists, "Metadata should not exist for extension without capability")
	})

	t.Run("delete metadata", func(t *testing.T) {
		// Ensure metadata exists first
		exists := manager.MetadataExists(extension.Id)
		require.True(t, exists)

		// Delete metadata
		err := manager.DeleteMetadata(extension.Id)
		require.NoError(t, err)

		// Verify metadata no longer exists
		exists = manager.MetadataExists(extension.Id)
		require.False(t, exists, "Metadata should not exist after deletion")
	})

	t.Run("load non-existent metadata returns error", func(t *testing.T) {
		_, err := manager.LoadMetadata("non.existent.extension")
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata not found")
	})

	t.Run("metadata command timeout does not fail operation", func(t *testing.T) {
		timeoutExtension := &Extension{
			Id:           "test.timeout.extension",
			Namespace:    "test",
			DisplayName:  "Test Timeout Extension",
			Version:      "1.0.0",
			Path:         relPath,
			Capabilities: []CapabilityType{MetadataCapability},
		}

		// Create extension directory
		timeoutExtDir := filepath.Join(tempDir, "extensions", timeoutExtension.Id)
		err := os.MkdirAll(timeoutExtDir, os.ModePerm)
		require.NoError(t, err)

		// Create a new mock context with isolated command runner
		timeoutMockContext := mocks.NewMockContext(t.Context())

		// Mock CommandRunner to simulate timeout for ANY metadata command
		timeoutMockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			// Match any command that has "metadata" as an argument
			return slices.Contains(args.Args, "metadata")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{}, context.DeadlineExceeded
		})

		// Create a new runner with the timeout mock context
		timeoutLazyRunner := lazy.NewLazy(func() (*Runner, error) {
			return NewRunner(timeoutMockContext.CommandRunner), nil
		})

		timeoutManager, err := NewManager(userConfigManager, sourceManager, timeoutLazyRunner, mockContext.HttpClient)
		require.NoError(t, err)

		// Fetch and cache metadata - should return error for timeout
		err = timeoutManager.fetchAndCacheMetadata(*mockContext.Context, timeoutExtension)
		require.Error(t, err, "Should return an error for timeout")
		require.Contains(t, err.Error(), "timed out", "Error should mention timeout")

		// Metadata should not exist since command timed out
		exists := timeoutManager.MetadataExists(timeoutExtension.Id)
		require.False(t, exists, "Metadata should not exist when command times out")

		// This demonstrates that installation would still succeed despite timeout
		// The actual Install() function logs this as a warning but doesn't fail
	})
}

func Test_GetInstalled_WithSourceFilter(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	mockContext := mocks.NewMockContext(t.Context())
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	userConfigManager := config.NewUserConfigManager(fileConfigManager)

	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})

	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install same extension ID from two different sources
	extensions := map[string]*Extension{
		"test.ext": {
			Id:          "test.ext",
			Namespace:   "test",
			DisplayName: "Test Extension",
			Version:     "1.0.0",
			Source:      "azd",
			Path:        "extensions/test.ext/test-ext",
		},
	}

	err = manager.userConfig.Set(installedConfigKey, extensions)
	require.NoError(t, err)

	t.Run("filter by ID only", func(t *testing.T) {
		ext, err := manager.GetInstalled(FilterOptions{Id: "test.ext"})
		require.NoError(t, err)
		require.NotNil(t, ext)
		require.Equal(t, "test.ext", ext.Id)
	})

	t.Run("filter by ID and Source", func(t *testing.T) {
		ext, err := manager.GetInstalled(FilterOptions{Id: "test.ext", Source: "azd"})
		require.NoError(t, err)
		require.NotNil(t, ext)
		require.Equal(t, "test.ext", ext.Id)
		require.Equal(t, "azd", ext.Source)
	})

	t.Run("filter by ID and wrong Source returns not found", func(t *testing.T) {
		_, err := manager.GetInstalled(FilterOptions{Id: "test.ext", Source: "local"})
		require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
	})

	t.Run("filter by non-existent ID returns not found", func(t *testing.T) {
		_, err := manager.GetInstalled(FilterOptions{Id: "non.existent"})
		require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
	})
}

func Test_CreateSourcesFromConfig_PartialSchemaFailure(t *testing.T) {
	tempDir := t.TempDir()

	// Create a compatible registry file (schema 1.0)
	compatibleRegistry := Registry{
		SchemaVersion: "1.0",
		Extensions: []*ExtensionMetadata{
			{
				Id:          "compat.extension",
				Namespace:   "compat",
				DisplayName: "Compatible Extension",
				Description: "An extension from a compatible source",
				Versions: []ExtensionVersion{{
					Version:   "1.0.0",
					Artifacts: sampleArtifacts,
				}},
			},
		},
	}
	compatData, err := json.Marshal(compatibleRegistry)
	require.NoError(t, err)
	compatFile := filepath.Join(tempDir, "compat-registry.json")
	require.NoError(t, os.WriteFile(compatFile, compatData, 0600))

	// Create an incompatible registry file (schema 2.0)
	incompatibleRegistry := map[string]any{
		"schemaVersion": "2.0",
		"extensions":    []any{},
	}
	incompatData, err := json.Marshal(incompatibleRegistry)
	require.NoError(t, err)
	incompatFile := filepath.Join(tempDir, "incompat-registry.json")
	require.NoError(t, os.WriteFile(
		incompatFile, incompatData, 0600,
	))

	mockContext := mocks.NewMockContext(context.Background())

	// Pre-populate config with only our file sources so the
	// default "azd" URL source is never auto-created.
	cfg, _ := mockContext.ConfigManager.Load("")
	require.NoError(t, cfg.Set("extension.sources.compatible",
		&SourceConfig{
			Name:     "compatible",
			Type:     SourceKindFile,
			Location: compatFile,
		},
	))
	require.NoError(t, cfg.Set("extension.sources.incompatible",
		&SourceConfig{
			Name:     "incompatible",
			Type:     SourceKindFile,
			Location: incompatFile,
		},
	))

	userConfigManager := config.NewUserConfigManager(
		mockContext.ConfigManager,
	)
	sourceManager := NewSourceManager(
		mockContext.Container,
		userConfigManager,
		mockContext.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(
		userConfigManager, sourceManager,
		lazyRunner, mockContext.HttpClient,
	)
	require.NoError(t, err)

	// FindExtensions should succeed — the compatible source works
	extensions, err := manager.FindExtensions(
		*mockContext.Context, nil,
	)
	require.NoError(t, err)
	require.Len(t, extensions, 1)
	require.Equal(t, "compat.extension", extensions[0].Id)
}

func Test_CreateSourcesFromConfig_AllSchemasIncompatible(t *testing.T) {
	tempDir := t.TempDir()

	// Create two incompatible registry files
	for _, name := range []string{
		"incompat1.json", "incompat2.json",
	} {
		registry := map[string]any{
			"schemaVersion": "2.0",
			"extensions":    []any{},
		}
		data, err := json.Marshal(registry)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(tempDir, name), data, 0600,
		))
	}

	mockContext := mocks.NewMockContext(context.Background())

	// Pre-populate config with only incompatible file sources.
	cfg, _ := mockContext.ConfigManager.Load("")
	require.NoError(t, cfg.Set("extension.sources.src1",
		&SourceConfig{
			Name:     "src1",
			Type:     SourceKindFile,
			Location: filepath.Join(tempDir, "incompat1.json"),
		},
	))
	require.NoError(t, cfg.Set("extension.sources.src2",
		&SourceConfig{
			Name:     "src2",
			Type:     SourceKindFile,
			Location: filepath.Join(tempDir, "incompat2.json"),
		},
	))

	userConfigManager := config.NewUserConfigManager(
		mockContext.ConfigManager,
	)
	sourceManager := NewSourceManager(
		mockContext.Container,
		userConfigManager,
		mockContext.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(
		userConfigManager, sourceManager,
		lazyRunner, mockContext.HttpClient,
	)
	require.NoError(t, err)

	// FindExtensions should fail — all sources are incompatible
	_, err = manager.FindExtensions(*mockContext.Context, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func Test_EntryPoint_PathTraversal_Blocked(t *testing.T) {
	// Test that path traversal in entryPoint is detected via the shared IsPathContained utility
	testCases := []struct {
		name        string
		entryPoint  string
		shouldFail  bool
		windowsOnly bool
	}{
		{"normal entry point", "myext.exe", false, false},
		{"subdirectory entry point", "bin/myext", false, false},
		{"path traversal attempt", "../../malicious", true, false},
		// While backslashes are traditionally path separators only on Windows, IsPathContained normalizes
		// them on all platforms and will treat `..\..` as a traversal sequence even on non-Windows.
		{"path traversal with backslash", `..\..\malicious`, true, false},
		{"double dot deep traversal", "../../../../../../../tmp/evil", true, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("backslash path traversal only applies on Windows")
			}
			baseDir := t.TempDir()
			targetPath := filepath.Join(baseDir, tc.entryPoint)
			isContained := osutil.IsPathContained(baseDir, targetPath)

			if tc.shouldFail {
				require.False(t, isContained, "path traversal should have been detected for %q", tc.entryPoint)
			} else {
				require.True(t, isContained, "legitimate entry point %q should be allowed", tc.entryPoint)
			}
		})
	}
}

func Test_CopyFromLocalPath_PathTraversal_Blocked(t *testing.T) {
	// Verify the containment check logic for relative artifact paths via shared IsPathContained
	testCases := []struct {
		name       string
		path       string
		shouldFail bool
	}{
		{"normal relative path", "extensions/myext.zip", false},
		{"path traversal attempt", "../../etc/passwd", true},
		{"path traversal with nested dirs", "../../../tmp/malicious", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			resolvedPath := filepath.Join(baseDir, tc.path)
			isContained := osutil.IsPathContained(baseDir, resolvedPath)

			if tc.shouldFail {
				require.False(t, isContained, "path traversal should have been detected for %q", tc.path)
			} else {
				require.True(t, isContained, "legitimate path %q should be allowed", tc.path)
			}
		})
	}
}

// Test_Upgrade_DependencyUpgrade verifies dependency upgrade decisions.
func Test_Upgrade_DependencyUpgrade_TightenedConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.pack",
				Namespace:   "test",
				DisplayName: "Test Pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id:          "test.child",
				Namespace:   "test",
				DisplayName: "Test Child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install pack v1, which pulls in child v1 (constrained to ~1.0.0).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Len(t, packs, 1)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	installed, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", installed.Version)

	// Upgrade pack to v2; child must upgrade from 1.0.0 to 2.0.0.
	packsV2, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Len(t, packsV2, 1)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packsV2[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "test.child", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusUpgraded, depUpgrades[0].Status)
	require.Equal(t, "1.0.0", depUpgrades[0].FromVersion)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)

	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", child.Version)
}

func Test_Upgrade_DependencyUpgrade_ConstraintAlreadySatisfied(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.5.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	// Child should be at 2.0.0 (latest >=1.0.0).
	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", child.Version)

	// Upgrading pack again with the same constraint should not upgrade the child.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions(""),
	)
	require.NoError(t, err)
	require.Empty(t, depUpgrades)
}

func Test_Upgrade_DependencyUpgrade_ReconcilesWhenParentCurrent(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	children, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, children[0], "1.0.0")
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	_, depUpgrades, err := manager.ReconcileDependencies(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions(""),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "test.child", depUpgrades[0].ExtensionId)
	require.Equal(t, "1.0.0", depUpgrades[0].FromVersion)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)
}

func Test_Upgrade_DependencyUpgrade_RefusesToDowngradeOutsideConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: "~1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "1.1.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install pack first; it pulls in test.child at 1.0.0 (the only version matching ~1.0.0).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	// Simulate the user upgrading the dependency standalone to a version outside the constraint.
	require.NoError(t, manager.Uninstall(t.Context(), "test.child"))
	children, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, children[0], "2.0.0")
	require.NoError(t, err)

	_, depUpgrades, err := manager.ReconcileDependencies(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions(""),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "test.child", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusSkipped, depUpgrades[0].Status)
	require.Equal(t, "2.0.0", depUpgrades[0].FromVersion)
	require.Contains(t, depUpgrades[0].SkipReason, "current 2.0.0")
	require.Contains(t, depUpgrades[0].SkipReason, "outside")
	require.Contains(t, depUpgrades[0].Suggestion, "azd ext install test.child --version 1.0.0")

	// Installed version should still be 2.0.0 — no downgrade happened.
	installed, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", installed.Version)
}

func Test_Upgrade_DependencyUpgrade_KeepsNewerInstalledWhenInRange(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "1.5.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", RequiredAzdVersion: ">=99.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Install the child at 2.0.0 first; it's outside the azd-compatible candidate set
	// ({1.0.0, 1.5.0}) but still satisfies the pack's >=1.0.0 constraint, so reconciliation
	// should keep it rather than downgrade to 1.5.0.
	children, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, children[0], "2.0.0")
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	azdVersion, err := semver.NewVersion("1.0.0")
	require.NoError(t, err)
	_, depUpgrades, err := manager.ReconcileDependencies(
		*mockContext.Context,
		packs[0],
		UpgradeOptions{
			VersionPreference:   "",
			UpgradeDependencies: true,
			AzdVersion:          azdVersion,
		},
	)
	require.NoError(t, err)
	require.Empty(t, depUpgrades)

	installed, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", installed.Version)
}

func Test_Upgrade_DependencyUpgrade_UsesCompatibleDependencyVersion(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=1.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", RequiredAzdVersion: ">=99.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	children, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, children[0], "1.0.0")
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	azdVersion, err := semver.NewVersion("1.0.0")
	require.NoError(t, err)
	_, depUpgrades, err := manager.ReconcileDependencies(
		*mockContext.Context,
		packs[0],
		UpgradeOptions{
			VersionPreference:   "",
			UpgradeDependencies: true,
			AzdVersion:          azdVersion,
		},
	)
	require.NoError(t, err)
	require.Empty(t, depUpgrades)
}

func Test_Upgrade_DependencyUpgrade_EmptyConstraint(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child"}, // empty constraint
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "9.9.9", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "")
	require.NoError(t, err)

	// Force the child to an older version to confirm dependency upgrade does NOT
	// run when the parent's dependency constraint is empty.
	_ = manager.Uninstall(t.Context(), "test.child")
	childMeta, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, childMeta[0], "1.0.0")
	require.NoError(t, err)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions(""),
	)
	require.NoError(t, err)
	require.Empty(t, depUpgrades)

	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", child.Version)
}

func Test_Upgrade_DependencyUpgrade_DisabledByOpts(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "test.pack",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "test.child", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "test.child",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		UpgradeOptions{VersionPreference: "2.0.0", UpgradeDependencies: false},
	)
	require.NoError(t, err)
	// Disabled dependency upgrades should surface as Skipped.
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "test.child", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusSkipped, depUpgrades[0].Status)
	require.Equal(t, "1.0.0", depUpgrades[0].FromVersion)
	require.Contains(t, depUpgrades[0].SkipReason, "dependency upgrades disabled")

	child, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", child.Version, "child must remain at 1.0.0 when dependency upgrades are disabled")
}

func Test_Upgrade_DependencyUpgrade_CycleGuard(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Registry shape avoids an install cycle but introduces one during upgrade.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version:   "1.0.0",
						Artifacts: sampleArtifacts,
					},
					{
						Version:   "2.0.0",
						Artifacts: sampleArtifacts,
						Dependencies: []ExtensionDependency{
							{Id: "pack.a", Version: ">=1.0.0"},
						},
					},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install pack.a v1 → pulls in pack.b v1 (no transitive deps).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	// Upgrade pack.a; pack.b v2 depends back on pack.a.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "pack.b", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusUpgraded, depUpgrades[0].Status)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)
	// pack.a is in the visited set, so pack.b's nested upgrade must skip it.
	require.Empty(t, depUpgrades[0].DependencyUpgrades, "nested dependency upgrade must respect visited set")
}

func Test_Upgrade_DependencyUpgrade_NestedPacks(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "leaf.c",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install A v1 → B v1 → C v1 (constraints pin each step to ~1.0.0).
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	bInstalled, err := manager.GetInstalled(FilterOptions{Id: "pack.b"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", bInstalled.Version)
	cInstalled, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", cInstalled.Version)

	// Upgrade A to v2; B and C should also upgrade to v2.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)
	require.Len(t, depUpgrades, 1)
	require.Equal(t, "pack.b", depUpgrades[0].ExtensionId)
	require.Equal(t, UpgradeStatusUpgraded, depUpgrades[0].Status)
	require.Equal(t, "2.0.0", depUpgrades[0].ToVersion)
	require.Len(t, depUpgrades[0].DependencyUpgrades, 1)
	require.Equal(t, "leaf.c", depUpgrades[0].DependencyUpgrades[0].ExtensionId)
	require.Equal(t, "2.0.0", depUpgrades[0].DependencyUpgrades[0].ToVersion)

	bFinal, err := manager.GetInstalled(FilterOptions{Id: "pack.b"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", bFinal.Version)
	cFinal, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", cFinal.Version)
}

func Test_Install_DependencyCycle_Bounded(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Registry defines a true install-time cycle: A → B → A.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "1.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.a", Version: "1.0.0"},
						},
					},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	require.Len(t, packs, 1)

	// Install must terminate (not recurse forever) and surface a cycle error.
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle detected")
}

func Test_Upgrade_DependencyUpgrade_ConstraintConflict(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// Pack A v2 depends on { B: ">=2.0.0", C: ">=2.0.0" }.
	// B v2 pins C to 1.x, which conflicts with A v2's >=2.0.0 constraint on C.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: "~1.0.0"},
							{Id: "leaf.c", Version: "~0.9.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "pack.b", Version: ">=2.0.0"},
							{Id: "leaf.c", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~0.9.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
						},
					},
				},
			},
			{
				Id: "leaf.c",
				Versions: []ExtensionVersion{
					{Version: "0.9.0", Artifacts: sampleArtifacts},
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install A v1 → B v1 → C v0.9. After this, all three are
	// installed and consistent with A v1's constraints.
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	cInstalled, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "0.9.0", cInstalled.Version)

	// Upgrade A to v2 — iteration order processes B first (sibling pin to
	// C ~1.0.0), then attempts to upgrade C to satisfy A's >=2.0.0.
	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)

	// Two top-level dependency-upgrade entries: B upgraded, C reported as a conflict.
	require.Len(t, depUpgrades, 2)

	var bEntry, cEntry *UpgradeResult
	for i := range depUpgrades {
		entry := &depUpgrades[i]
		switch entry.ExtensionId {
		case "pack.b":
			bEntry = entry
		case "leaf.c":
			cEntry = entry
		}
	}
	require.NotNil(t, bEntry, "pack.b dependency-upgrade entry expected")
	require.NotNil(t, cEntry, "leaf.c dependency-upgrade entry expected")

	require.Equal(t, UpgradeStatusUpgraded, bEntry.Status)
	require.Equal(t, "2.0.0", bEntry.ToVersion)

	require.Equal(t, UpgradeStatusFailed, cEntry.Status)
	require.Error(t, cEntry.Error)
	require.Contains(t, cEntry.Error.Error(), "constraint conflict")
	require.Contains(t, cEntry.Error.Error(), "leaf.c")
}

// Test_Upgrade_DependencyUpgrade_NoOpStillPinsForSiblings exercises the case
// where the first parent processed for a dependency leaves it untouched (the
// installed version already matches the highest satisfying version). A later
// sibling upgrade chain whose constraint is incompatible with that installed
// version must still surface a constraint conflict, rather than silently
// picking its own preferred version and invalidating the first parent's
// constraint.
func Test_Upgrade_DependencyUpgrade_NoOpStillPinsForSiblings(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	// pack.a v2 lists leaf.c first (already at 1.0.0, satisfies ~1.0.0 → no-op)
	// and pack.b second. pack.b v2 wants leaf.c >=2.0.0, which would normally
	// trigger an upgrade of leaf.c — but pack.a's earlier no-op visit must
	// have pinned leaf.c so that pack.b's recursive walk surfaces a conflict
	// instead of silently upgrading leaf.c out of pack.a's range.
	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: "pack.a",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
							{Id: "pack.b", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
							{Id: "pack.b", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "pack.b",
				Versions: []ExtensionVersion{
					{
						Version: "1.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: "~1.0.0"},
						},
					},
					{
						Version: "2.0.0",
						Dependencies: []ExtensionDependency{
							{Id: "leaf.c", Version: ">=2.0.0"},
						},
					},
				},
			},
			{
				Id: "leaf.c",
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: sampleArtifacts},
					{Version: "2.0.0", Artifacts: sampleArtifacts},
				},
			},
		},
	}

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == extensionRegistryUrl
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	// Bootstrap: install pack.a v1 → pack.b v1 → leaf.c 1.0.0.
	packs, err := manager.FindExtensions(*mockContext.Context, &FilterOptions{Id: "pack.a"})
	require.NoError(t, err)
	_, err = manager.Install(*mockContext.Context, packs[0], "1.0.0")
	require.NoError(t, err)

	cInstalled, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", cInstalled.Version)

	_, depUpgrades, err := manager.Upgrade(
		*mockContext.Context,
		packs[0],
		DefaultUpgradeOptions("2.0.0"),
	)
	require.NoError(t, err)

	var cEntry *UpgradeResult
	var findInNested func(entries []UpgradeResult)
	findInNested = func(entries []UpgradeResult) {
		for i := range entries {
			if entries[i].ExtensionId == "leaf.c" {
				cEntry = &entries[i]
				return
			}
			findInNested(entries[i].DependencyUpgrades)
			if cEntry != nil {
				return
			}
		}
	}
	findInNested(depUpgrades)

	require.NotNil(t, cEntry, "leaf.c dependency-upgrade entry expected")
	require.Equal(t, UpgradeStatusFailed, cEntry.Status)
	require.Error(t, cEntry.Error)
	require.Contains(t, cEntry.Error.Error(), "constraint conflict")

	// leaf.c must remain at 1.0.0 — pack.a's earlier no-op visit pinned it.
	cFinal, err := manager.GetInstalled(FilterOptions{Id: "leaf.c"})
	require.NoError(t, err)
	require.Equal(t, "1.0.0", cFinal.Version)
}

func Test_DependencyNotFoundError(t *testing.T) {
	t.Parallel()

	err := &DependencyNotFoundError{DependencyId: "azure.ai.inspector", ParentId: "azure.ai.agents"}
	require.Contains(t, err.Error(), "azure.ai.inspector")
	require.Contains(t, err.Error(), "azure.ai.agents")

	// Unwraps correctly through fmt.Errorf chains.
	wrapped := fmt.Errorf("install failed: %w", err)
	typed, ok := errorsAsDependencyNotFound(wrapped)
	require.True(t, ok)
	require.Equal(t, "azure.ai.inspector", typed.DependencyId)
}

func errorsAsDependencyNotFound(err error) (*DependencyNotFoundError, bool) {
	return errors.AsType[*DependencyNotFoundError](err)
}

func Test_DependencyAmbiguousSourceError(t *testing.T) {
	t.Parallel()

	err := &DependencyAmbiguousSourceError{
		DependencyId: "azure.ai.inspector",
		ParentId:     "azure.ai.agents",
		Sources:      []string{"azd", "dev"},
	}
	msg := err.Error()
	require.Contains(t, msg, "azure.ai.inspector")
	require.Contains(t, msg, "azure.ai.agents")
	require.Contains(t, msg, "azd, dev")

	// Degrades gracefully when no sources are captured.
	noSources := &DependencyAmbiguousSourceError{DependencyId: "x", ParentId: "y"}
	require.Contains(t, noSources.Error(), "multiple sources")

	// Unwraps correctly through fmt.Errorf chains.
	wrapped := fmt.Errorf("install failed: %w", err)
	typed, ok := errors.AsType[*DependencyAmbiguousSourceError](wrapped)
	require.True(t, ok)
	require.Equal(t, []string{"azd", "dev"}, typed.Sources)
}

func Test_dependencySources(t *testing.T) {
	t.Parallel()

	matches := []*ExtensionMetadata{
		{Id: "x", Source: "dev"},
		{Id: "x", Source: "azd"},
		{Id: "x", Source: "dev"}, // duplicate
		{Id: "x", Source: ""},    // empty ignored
	}
	require.Equal(t, []string{"azd", "dev"}, dependencySources(matches))
}
