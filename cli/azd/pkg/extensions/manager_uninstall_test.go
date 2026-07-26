// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/processutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/proctest"
	"github.com/stretchr/testify/require"
)

const (
	// discoveryTimeout is how long a freshly started helper is given to show up in
	// process enumeration.
	discoveryTimeout = 30 * time.Second

	// discoveryPollInterval is how often process enumeration is retried while waiting.
	discoveryPollInterval = 100 * time.Millisecond

	// trashSweepTimeout bounds how long a test waits for the operating system to release
	// a terminated process's executable image so relocated files become deletable.
	trashSweepTimeout = 10 * time.Second
)

func TestMain(m *testing.M) {
	if proctest.RunRequestedHelper() {
		return
	}

	os.Exit(m.Run())
}

// installedExtensionFixture is a registered extension whose directory contains a real
// running executable, which is the situation that makes upgrade and uninstall fail today.
type installedExtensionFixture struct {
	manager      *Manager
	id           string
	extensionDir string
	trashDir     string
	process      *proctest.Handle
}

// newRunningExtension registers an extension in user config, places a real executable in
// its install directory, and starts it.
//
// Only a genuine process gives the operating system semantics that matter here: on
// Windows the running image cannot be deleted, and on every platform the process has to
// be discoverable by executable path.
func newRunningExtension(t *testing.T, id string) *installedExtensionFixture {
	t.Helper()

	configDir := isolatedConfigDir(t)
	extensionDir := filepath.Join(configDir, extensionsDirName, id)
	require.NoError(t, os.MkdirAll(extensionDir, osutil.PermissionDirectory))

	process := proctest.StartIn(t, extensionDir, helperExecutableBaseName(id), proctest.ModeIdle)

	manager := newTestManager(t)
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		id: {
			Id:      id,
			Version: "1.0.0",
			Path:    filepath.Join(extensionsDirName, id, filepath.Base(process.Path)),
		},
	}))

	requireDiscoverable(t, extensionDir, process.PID)

	return &installedExtensionFixture{
		manager:      manager,
		id:           id,
		extensionDir: extensionDir,
		trashDir:     filepath.Join(configDir, extensionsDirName, trashDirName),
		process:      process,
	}
}

// isolatedConfigDir redirects azd configuration at a temporary directory and proves the
// redirect took effect, so a failing test can never write into or delete from the
// developer's real configuration.
func isolatedConfigDir(t *testing.T) string {
	t.Helper()

	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	resolved, err := config.GetUserConfigDir()
	require.NoError(t, err)
	require.Equal(t, configDir, resolved)

	return configDir
}

// requireDiscoverable asserts the process is visible through the same lookup azd uses, so
// a test that later claims "--force stopped it" cannot pass vacuously against a process
// that was never found in the first place.
func requireDiscoverable(t *testing.T, extensionDir string, pid int) {
	t.Helper()

	require.Eventually(t, func() bool {
		running, err := processutil.FindByExecutableDir(t.Context(), extensionDir)
		if err != nil {
			return false
		}

		for _, process := range running {
			if process.PID == pid {
				return true
			}
		}

		return false
	}, discoveryTimeout, discoveryPollInterval, "extension process never became discoverable")
}

// helperExecutableBaseName derives a per-extension executable name, so process
// enumeration can attribute a match to the extension it came from.
func helperExecutableBaseName(id string) string {
	return "azd-ext-" + strings.ReplaceAll(id, ".", "-")
}

// helperExecutableName is the on-disk file name of the stand-in extension executable.
func helperExecutableName(id string) string {
	return proctest.ExecutableName(helperExecutableBaseName(id))
}

// T25: the default path. Uninstall must succeed while the extension is running, which is
// exactly what fails today with a bare access denied on Windows.
func TestUninstall_SucceedsWithLockedBinary(t *testing.T) {
	fixture := newRunningExtension(t, "test.running")

	require.NoError(t, fixture.manager.Uninstall(t.Context(), fixture.id))
	require.NoDirExists(t, fixture.extensionDir,
		"the extension directory must be gone even though the extension is running")

	installed, err := fixture.manager.ListInstalled()
	require.NoError(t, err)
	require.NotContains(t, installed, fixture.id)

	// The default path is non-destructive: the running process is left alone.
	require.False(t, fixture.process.HasExited(), "uninstall without --force must not stop anything")
}

// T26 and T30: with Force the extension's own processes are stopped before removal, so
// the removal is complete rather than deferred, and each stopped process is reported.
func TestUninstall_ForceTerminatesProcesses(t *testing.T) {
	fixture := newRunningExtension(t, "test.forced")

	var stopped []processutil.ProcessInfo
	err := fixture.manager.UninstallWithOptions(t.Context(), fixture.id, UninstallOptions{
		Force: true,
		OnProcessStopped: func(process processutil.ProcessInfo) {
			stopped = append(stopped, process)
		},
	})
	require.NoError(t, err)

	require.NoDirExists(t, fixture.extensionDir)
	fixture.process.RequireStopped(t)

	// The stopped process is reported, so the user can see what azd did on their behalf
	// rather than having it happen silently.
	require.Len(t, stopped, 1)
	require.Equal(t, fixture.process.PID, stopped[0].PID)
	require.Equal(t, helperExecutableName(fixture.id), stopped[0].Name)
	require.Contains(t, stopped[0].String(), "PID")

	// A forced removal must not leave anything permanent behind. Anything moved aside
	// because the operating system had not finished releasing the terminated process is
	// deletable once it has, so a sweep clears it rather than it lingering forever.
	require.Eventually(t, func() bool {
		osutil.SweepTrash(t.Context(), fixture.trashDir)

		_, err := os.Stat(fixture.trashDir)

		return errors.Is(err, os.ErrNotExist)
	}, trashSweepTimeout, discoveryPollInterval, "forced uninstall left permanent trash behind")
}

// T29: without Force, azd must never terminate anything. This is the safety default and
// the reason the destructive behavior is opt-in.
func TestUninstall_NoForceNeverTerminates(t *testing.T) {
	fixture := newRunningExtension(t, "test.untouched")

	called := false
	err := fixture.manager.UninstallWithOptions(t.Context(), fixture.id, UninstallOptions{
		OnProcessStopped: func(processutil.ProcessInfo) {
			called = true
		},
	})
	require.NoError(t, err)

	require.False(t, called, "no process should be reported as stopped without --force")
	require.False(t, fixture.process.HasExited(), "the extension process must survive")
}

// T28: a blocked removal must say what is holding the files and how to get past it,
// instead of surfacing a bare permission error.
func TestUninstall_BlockedErrorNamesProcesses(t *testing.T) {
	fixture := newRunningExtension(t, "test.blocked")

	cause := os.ErrPermission

	unforced := extensionRemovalError(t.Context(), fixture.extensionDir, false, cause)
	require.ErrorIs(t, unforced, cause)
	require.ErrorContains(t, unforced, helperExecutableName(fixture.id))
	require.ErrorContains(t, unforced, "PID")
	require.Contains(t, suggestionOf(t, unforced), "--force",
		"an unforced failure should point at the flag that resolves it")

	forced := extensionRemovalError(t.Context(), fixture.extensionDir, true, cause)
	require.ErrorIs(t, forced, cause)
	require.ErrorContains(t, forced, helperExecutableName(fixture.id))
	require.NotContains(t, suggestionOf(t, forced), "--force",
		"suggesting --force to someone who already used it is not actionable")
}

// A removal that failed for a reason other than a running process must not be dressed up
// as one, or the guidance would send the user chasing a process that does not exist.
func TestUninstall_BlockedErrorWithoutProcesses(t *testing.T) {
	t.Parallel()

	err := extensionRemovalError(t.Context(), filepath.Join(t.TempDir(), "empty"), false, os.ErrPermission)
	require.ErrorIs(t, err, os.ErrPermission)
	require.ErrorContains(t, err, "failed to remove extension")
	require.NotContains(t, err.Error(), "--force")
}

// stopExtensionProcesses must refuse an unscoped directory rather than ranging wider than
// the extension it was asked about.
func TestStopExtensionProcesses_RejectsUnscopedDirectory(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, stopExtensionProcesses(t.Context(), "", nil), processutil.ErrEmptyDirectory)

	root := "/"
	if runtime.GOOS == "windows" {
		root = `C:\`
	}

	require.ErrorIs(t, stopExtensionProcesses(t.Context(), root, nil), processutil.ErrRootDirectory)
}

// A directory with nothing running in it is a no-op, not an error.
func TestStopExtensionProcesses_NoProcesses(t *testing.T) {
	t.Parallel()

	called := false
	err := stopExtensionProcesses(t.Context(), t.TempDir(), func(processutil.ProcessInfo) {
		called = true
	})

	require.NoError(t, err)
	require.False(t, called)
}

// The extension id decides which directory azd deletes and, under --force, which
// directory scopes process termination. Ids come from registry JSON, so a traversing id
// must be refused at the choke point rather than relied on to be well formed.
func TestExtensionPaths_RejectsUnsafeIds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	unsafe := []struct {
		name string
		id   string
	}{
		{"parent traversal", ".."},
		{"repeated traversal", filepath.Join("..", "..")},
		{"traversal into a sibling tree", filepath.Join("..", "..", "Program Files", "App")},
		{"nested path", filepath.Join("a", "b")},
		{"empty", ""},
		{"dot", "."},
		{"reserved trash directory", trashDirName},
		{"reserved trash directory, different case", strings.ToUpper(trashDirName)},

		// Windows strips trailing dots and spaces from a path component, so these all
		// resolve onto a directory whose name is not the one checked above. Verified on
		// Windows 11: MkdirAll of each of these produces the stripped name.
		{"reserved trash directory, trailing dot", trashDirName + "."},
		{"reserved trash directory, trailing space", trashDirName + " "},
		{"trailing dot", "test.demo."},
		{"trailing dots", "test.demo.."},
		{"trailing space", "test.demo "},

		{"drive letter", "C:"},
		{"alternate data stream", "test.demo:stream"},
		{"null byte", "test\x00demo"},
		{"control character", "test\tdemo"},

		// Reserved device names resolve to a device rather than a directory in any
		// location, with or without an extension.
		{"reserved device name", "CON"},
		{"reserved device name, different case", "nul"},
		{"reserved device name with an extension", "COM1.exe"},
	}

	for _, testCase := range unsafe {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := extensionPaths(root, testCase.id)
			require.ErrorIs(t, err, ErrInvalidExtensionId,
				"id %q must not resolve to a usable directory", testCase.id)
		})
	}
}

// A normal id must still resolve, and it must land directly under the extensions root
// alongside the trash directory it will be swept into.
func TestExtensionPaths_AcceptsNormalId(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionsRoot := filepath.Join(root, extensionsDirName)

	extensionDir, trashDir, err := extensionPaths(root, "microsoft.azd.demo")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(extensionsRoot, "microsoft.azd.demo"), extensionDir)
	require.Equal(t, filepath.Join(extensionsRoot, trashDirName), trashDir)
}

// Aliasing works in both directions: rejecting "test.demo." is only useful because
// "test.demo" is a real id that another extension may already own. Without the trailing
// character check, uninstalling one would remove the other's files.
func TestExtensionPaths_AliasedIdWouldResolveOntoAnotherExtension(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	victim, _, err := extensionPaths(root, "test.demo")
	require.NoError(t, err)

	_, _, err = extensionPaths(root, "test.demo.")
	require.ErrorIs(t, err, ErrInvalidExtensionId)

	// The rejected id would have resolved onto exactly the victim's directory, because
	// Windows discards the trailing dot when it creates or opens the path.
	require.Equal(t, victim, filepath.Join(root, extensionsDirName, "test.demo"))
}

// T27: Force has to survive the trip from UpgradeOptions through upgradeInternal into the
// uninstall step, or the flag would be accepted on upgrade and quietly do nothing. This
// drives a real upgrade rather than the uninstall it delegates to.
func TestUpgrade_PropagatesForce(t *testing.T) {
	fixture := newUpgradableExtension(t, "test.upgrade-forced")

	var stopped []processutil.ProcessInfo
	_, _, err := fixture.manager.Upgrade(t.Context(), fixture.metadata, UpgradeOptions{
		VersionPreference: "2.0.0",
		SkipDependencies:  true,
		Force:             true,
		OnProcessStopped: func(process processutil.ProcessInfo) {
			stopped = append(stopped, process)
		},
	})
	require.NoError(t, err)

	require.Len(t, stopped, 1, "the upgrade path must forward Force into the uninstall step")
	require.Equal(t, fixture.process.PID, stopped[0].PID)
	fixture.process.RequireStopped(t)

	upgraded, err := fixture.manager.GetInstalled(FilterOptions{Id: fixture.id})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", upgraded.Version)
}

// The headline fix: an upgrade must succeed while the extension is running, without
// --force and without stopping anything. This is the case that fails today.
func TestUpgrade_SucceedsWhileExtensionIsRunning(t *testing.T) {
	fixture := newUpgradableExtension(t, "test.upgrade-running")

	called := false
	_, _, err := fixture.manager.Upgrade(t.Context(), fixture.metadata, UpgradeOptions{
		VersionPreference: "2.0.0",
		SkipDependencies:  true,
		OnProcessStopped: func(processutil.ProcessInfo) {
			called = true
		},
	})
	require.NoError(t, err)

	require.False(t, called, "the default upgrade must not stop anything")
	require.False(t, fixture.process.HasExited(), "the running extension must survive its own upgrade")

	upgraded, err := fixture.manager.GetInstalled(FilterOptions{Id: fixture.id})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", upgraded.Version)

	// The newly installed artifact is in place, which is the point of the upgrade.
	require.FileExists(t, filepath.Join(fixture.extensionDir, upgraded.Version+".txt"))
}

// upgradableExtension is a genuinely installed extension, backed by a mock registry that
// offers a newer version, with a real process running out of its install directory.
type upgradableExtension struct {
	manager      *Manager
	metadata     *ExtensionMetadata
	id           string
	extensionDir string
	process      *proctest.Handle
}

func newUpgradableExtension(t *testing.T, id string) *upgradableExtension {
	t.Helper()

	configDir := isolatedConfigDir(t)
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: id,
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: versionedArtifacts("1.0.0")},
					{Version: "2.0.0", Artifacts: versionedArtifacts("2.0.0")},
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

	found, err := manager.FindExtensions(t.Context(), &FilterOptions{Id: id})
	require.NoError(t, err)
	require.Len(t, found, 1)

	_, err = manager.InstallWithOptions(t.Context(), found[0], InstallOptions{
		VersionPreference: "1.0.0",
		SkipDependencies:  true,
	})
	require.NoError(t, err)

	// The registry artifact is inert test data, so a real executable is started from the
	// installed directory to reproduce a running extension.
	extensionDir := filepath.Join(configDir, extensionsDirName, id)
	process := proctest.StartIn(t, extensionDir, helperExecutableBaseName(id), proctest.ModeIdle)
	requireDiscoverable(t, extensionDir, process.PID)

	return &upgradableExtension{
		manager:      manager,
		metadata:     found[0],
		id:           id,
		extensionDir: extensionDir,
		process:      process,
	}
}

// A dependency's binary holds a file lock exactly like a top level extension's does, so
// --force has to reach it too. Before this was wired, upgrading a pack with --force
// stopped only the parent's processes and left the dependency's running, which made the
// flag silently cover part of what the user asked for.
func TestUpgrade_PropagatesForceToDependencies(t *testing.T) {
	const parentId = "test.pack-forced"
	const childId = "test.child-forced"

	configDir := isolatedConfigDir(t)
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id: parentId,
				Versions: []ExtensionVersion{
					{
						Version:      "1.0.0",
						Artifacts:    versionedArtifacts("1.0.0"),
						Dependencies: []ExtensionDependency{{Id: childId, Version: "~1.0.0"}},
					},
					{
						Version:      "2.0.0",
						Artifacts:    versionedArtifacts("2.0.0"),
						Dependencies: []ExtensionDependency{{Id: childId, Version: ">=2.0.0"}},
					},
				},
			},
			{
				Id: childId,
				Versions: []ExtensionVersion{
					{Version: "1.0.0", Artifacts: versionedArtifacts("1.0.0")},
					{Version: "2.0.0", Artifacts: versionedArtifacts("2.0.0")},
				},
			},
		},
	}

	manager := registryBackedManager(t, mockContext, registry)

	found, err := manager.FindExtensions(t.Context(), &FilterOptions{Id: parentId})
	require.NoError(t, err)
	require.Len(t, found, 1)

	_, err = manager.InstallWithOptions(t.Context(), found[0], InstallOptions{VersionPreference: "1.0.0"})
	require.NoError(t, err)

	// The process runs out of the dependency's directory, not the parent's, so only a
	// Force that reaches the child upgrade can stop it.
	childDir := filepath.Join(configDir, extensionsDirName, childId)
	childProcess := proctest.StartIn(t, childDir, helperExecutableBaseName(childId), proctest.ModeIdle)
	requireDiscoverable(t, childDir, childProcess.PID)

	var stopped []processutil.ProcessInfo
	_, _, err = manager.Upgrade(t.Context(), found[0], UpgradeOptions{
		VersionPreference:   "2.0.0",
		UpgradeDependencies: true,
		Force:               true,
		OnProcessStopped: func(process processutil.ProcessInfo) {
			stopped = append(stopped, process)
		},
	})
	require.NoError(t, err)

	require.Len(t, stopped, 1, "--force must reach the dependency upgrade, not just the parent")
	require.Equal(t, childProcess.PID, stopped[0].PID)
	childProcess.RequireStopped(t)
}

// Sweeping happens on the way in as well as the way out. A trash entry left behind by an
// earlier run (its process now gone) must not survive the next uninstall, otherwise
// --force would slowly accumulate dead binaries in the user's config directory.
func TestUninstall_SweepsPreexistingTrash(t *testing.T) {
	fixture := newRunningExtension(t, "test.uninstall-sweeps-existing")
	fixture.process.Stop()

	require.NoError(t, os.MkdirAll(fixture.trashDir, 0o755))
	stale := filepath.Join(fixture.trashDir, "stale-from-a-previous-run.bin")
	require.NoError(t, os.WriteFile(stale, []byte("dead binary"), 0o600))

	require.NoError(t, fixture.manager.Uninstall(t.Context(), fixture.id))

	require.NoFileExists(t, stale, "uninstall must sweep trash left by an earlier run")
}

// Install recreates the extension directory, and it sweeps on the way through for the same
// reason uninstall does. This is the path that finally clears a binary whose process was
// still holding it when the previous uninstall ran.
func TestInstall_SweepsPreexistingTrash(t *testing.T) {
	const id = "test.install-sweeps-existing"

	configDir := isolatedConfigDir(t)
	mockContext := mocks.NewMockContext(t.Context())

	registry := Registry{
		Extensions: []*ExtensionMetadata{
			{
				Id:       id,
				Versions: []ExtensionVersion{{Version: "1.0.0", Artifacts: versionedArtifacts("1.0.0")}},
			},
		},
	}

	manager := registryBackedManager(t, mockContext, registry)

	trashDir := filepath.Join(configDir, extensionsDirName, trashDirName)
	require.NoError(t, os.MkdirAll(trashDir, 0o755))
	stale := filepath.Join(trashDir, "stale-from-a-previous-run.bin")
	require.NoError(t, os.WriteFile(stale, []byte("dead binary"), 0o600))

	found, err := manager.FindExtensions(t.Context(), &FilterOptions{Id: id})
	require.NoError(t, err)
	require.Len(t, found, 1)

	_, err = manager.InstallWithOptions(t.Context(), found[0], InstallOptions{SkipDependencies: true})
	require.NoError(t, err)

	require.NoFileExists(t, stale, "install must sweep trash left by an earlier run")
}

// registryBackedManager builds a Manager whose registry is served from the supplied value,
// which is the shape every fixture in this file needs.
func registryBackedManager(t *testing.T, mockContext *mocks.MockContext, registry Registry) *Manager {
	t.Helper()

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

	return manager
}

// versionedArtifacts gives each version a distinct artifact filename, so a test can tell
// by inspection whether the new version actually landed on disk.
func versionedArtifacts(version string) map[string]ExtensionArtifact {
	artifacts := map[string]ExtensionArtifact{}
	for _, platform := range []string{"windows", "linux", "darwin"} {
		artifacts[platform] = ExtensionArtifact{
			URL: "https://aka.ms/azd/extensions/registry/test.extension/" + version + ".txt",
			AdditionalMetadata: map[string]any{
				"entryPoint": version + ".txt",
			},
		}
	}

	return artifacts
}

// suggestionOf extracts the actionable guidance attached to an error, if any.
func suggestionOf(t *testing.T, err error) string {
	t.Helper()

	if withSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
		return withSuggestion.Suggestion
	}

	return ""
}
