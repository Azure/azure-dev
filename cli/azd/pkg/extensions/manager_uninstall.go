// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/processutil"
)

// UninstallOptions controls how Manager.Uninstall behaves.
type UninstallOptions struct {
	// Force stops processes that are running out of the extension's install directory
	// when they would otherwise block its removal. Only executables inside that directory
	// are ever candidates, so nothing else on the machine is affected.
	Force bool

	// OnProcessStopped, when set, is called once for each process stopped because of
	// Force. It lets callers report what was stopped without Uninstall needing a return
	// value that every caller has to thread through.
	OnProcessStopped func(processutil.ProcessInfo)
}

// Uninstall uninstalls an extension by name.
//
// Removal succeeds even when the extension is running. Files that a live process holds
// open are moved aside rather than deleted, which is enough to empty and remove the
// extension directory. Those leftovers are swept on a later extension operation once the
// process exits. Use UninstallWithOptions to stop the extension's processes first, so
// the removal is complete rather than deferred.
func (m *Manager) Uninstall(ctx context.Context, id string) error {
	return m.uninstallInternal(ctx, id, UninstallOptions{})
}

// UninstallWithOptions uninstalls an extension by name, honoring UninstallOptions.
func (m *Manager) UninstallWithOptions(ctx context.Context, id string, opts UninstallOptions) error {
	return m.uninstallInternal(ctx, id, opts)
}

func (m *Manager) uninstallInternal(ctx context.Context, id string, opts UninstallOptions) error {
	// Get the installed extension
	extension, err := m.GetInstalled(FilterOptions{Id: id})
	if err != nil {
		return fmt.Errorf("failed to get installed extension: %w", err)
	}

	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir, trashDir, err := extensionPaths(userConfigDir, extension.Id)
	if err != nil {
		return err
	}

	// Checked before anything reads, deletes, or terminates against these paths, so a
	// link planted at either directory cannot redirect the removal or the kill scope.
	if err := requireRealExtensionDirs(extensionDir); err != nil {
		return err
	}

	// Clear anything an earlier run had to leave behind before potentially adding to it.
	osutil.SweepTrash(ctx, trashDir)

	if opts.Force {
		if err := stopExtensionProcesses(ctx, extensionDir, opts.OnProcessStopped); err != nil {
			return err
		}
	}

	if err := osutil.RemoveAllWithRelocation(ctx, extensionDir, trashDir); err != nil {
		return extensionRemovalError(ctx, extensionDir, opts.Force, err)
	}

	// A file held open at the moment of removal was moved aside rather than deleted.
	// Windows releases a terminated process's image a short moment after the process
	// object goes away, so sweeping again here usually clears what was just relocated
	// instead of leaving it for the next command to find.
	osutil.SweepTrash(ctx, trashDir)

	// Update the user config
	extensions, err := m.ListInstalled()
	if err != nil {
		return fmt.Errorf("failed to list installed extensions: %w", err)
	}

	delete(extensions, id)

	if err := m.userConfig.Set(installedConfigKey, extensions); err != nil {
		return fmt.Errorf("failed to set extensions section: %w", err)
	}

	if err := m.configManager.Save(m.userConfig); err != nil {
		return fmt.Errorf("failed to save user config: %w", err)
	}

	log.Printf("Extension '%s' uninstalled successfully\n", id)
	return nil
}

// extensionPaths resolves the install and trash directories for an extension id, refusing
// any id that is not a single directory name directly under the extensions root.
//
// Extension ids come from registry JSON and are persisted verbatim, so nothing upstream
// guarantees they are safe path components. filepath.Join cleans its result, which means
// an id like ".." silently resolves to a directory outside the extensions root and no
// later containment check can tell the difference. That matters most for --force, which
// hands this directory to process termination, but it also guards the removal path.
func extensionPaths(userConfigDir string, id string) (extensionDir string, trashDir string, err error) {
	if err := validateExtensionId(id); err != nil {
		return "", "", err
	}

	extensionsRoot := filepath.Join(userConfigDir, extensionsDirName)
	extensionDir = filepath.Join(extensionsRoot, id)

	// Requiring the parent to be exactly the extensions root rejects traversal ("..",
	// "a/b") and the empty or dot ids that would otherwise resolve to the root itself.
	// A containment check alone accepts all of those.
	if filepath.Dir(extensionDir) != extensionsRoot {
		return "", "", fmt.Errorf(
			"%w: %q must be a single directory name", ErrInvalidExtensionId, id)
	}

	return extensionDir, filepath.Join(extensionsRoot, trashDirName), nil
}

// requireRealExtensionDirs refuses to operate when either directory azd owns under the
// user config directory has been replaced by a symlink or junction.
//
// osutil.RequireRealDir deliberately inspects only a path's final component, so checking
// the extension directory alone is not enough: when the extensions root itself is a link,
// the operating system resolves it while walking to the final component and the check
// passes against the link target. Everything downstream then follows: the termination
// scope widens to whatever the link points at, the trash sweep deletes children there,
// and an install writes there. Both components azd creates therefore have to be checked.
//
// Only those two are checked. Symlinked ancestors above the config directory stay legal,
// which macOS requires: its configuration directory sits under /var while processes
// report their executables under /private/var.
func requireRealExtensionDirs(extensionDir string) error {
	if err := osutil.RequireRealDir(filepath.Dir(extensionDir)); err != nil {
		return err
	}

	return osutil.RequireRealDir(extensionDir)
}

// validateExtensionId rejects ids that cannot safely become a directory name, before the
// id is ever joined onto a path.
//
// The lexical parent check in extensionPaths is necessary but not sufficient on Windows,
// because Win32 silently strips trailing dots and spaces from path components. Go's
// filepath package does not model that, so filepath.Base(".trash.") is ".trash.", which
// slips past a comparison against the reserved name while the operating system creates,
// walks, and deletes the real ".trash" directory. The same aliasing lets the id "foo."
// resolve onto a different extension's directory, so an uninstall of one id can remove
// another extension's files. Verified on Windows 11: MkdirAll of ".trash.", ".trash ",
// "foo." and "foo.." all produce ".trash" and "foo".
//
// Ids are matched against what a path component may contain rather than against a list of
// known-bad shapes, so a form nobody thought of fails closed.
func validateExtensionId(id string) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %q %s", ErrInvalidExtensionId, id, reason)
	}

	if id == "" || id == "." || id == ".." {
		return invalid("must be a single directory name")
	}

	if strings.ContainsAny(id, `/\:`) {
		return invalid("must not contain a path separator or drive letter")
	}

	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return invalid("must not contain control characters")
		}
	}

	// Windows resolves these away, so the name azd checks is not the name it operates on.
	if trimmed := strings.TrimRight(id, ". "); trimmed != id {
		return invalid("must not end with a dot or space")
	}

	if strings.EqualFold(id, trashDirName) {
		return invalid("is reserved")
	}

	// Reserved device names resolve to devices rather than files, with or without an
	// extension, so "CON" and "CON.exe" are both refused.
	base, _, _ := strings.Cut(id, ".")
	if _, reserved := windowsDeviceNames[strings.ToUpper(base)]; reserved {
		return invalid("is a reserved device name")
	}

	return nil
}

// windowsDeviceNames are the legacy DOS device names Windows still resolves in any
// directory. They are rejected on every platform so an extension id cannot behave
// differently depending on where azd runs.
var windowsDeviceNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM0": {}, "COM1": {}, "COM2": {}, "COM3": {}, "COM4": {},
	"COM5": {}, "COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT0": {}, "LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {},
	"LPT5": {}, "LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// stopExtensionProcesses stops every process running out of the extension's install
// directory, reporting each one through onStopped as it goes.
//
// Discovery is scoped to extensionDir, so a process is only ever a candidate when its
// executable image lives inside the extension azd is operating on. Child processes the
// extension spawned are deliberately left alone: their executables live elsewhere, which
// puts them outside the containment guarantee that makes stopping anything defensible.
func stopExtensionProcesses(
	ctx context.Context,
	extensionDir string,
	onStopped func(processutil.ProcessInfo),
) error {
	running, err := processutil.FindByExecutableDir(ctx, extensionDir)
	if err != nil {
		return fmt.Errorf("failed to find running extension processes: %w", err)
	}

	for _, process := range running {
		stopped, err := processutil.Terminate(ctx, process, extensionDir, processutil.DefaultGracePeriod)
		if err != nil {
			return fmt.Errorf("failed to stop %s: %w", process, err)
		}

		// A process that exited on its own between discovery and termination is fine,
		// but azd did not stop it and must not claim it did.
		if !stopped {
			log.Printf("%s already exited before it could be stopped\n", process)

			continue
		}

		log.Printf("stopped %s to release files in %s\n", process, extensionDir)

		if onStopped != nil {
			onStopped(process)
		}
	}

	return nil
}

// extensionRemovalError turns a failed removal into something the user can act on.
//
// A bare "Access is denied" says nothing about what is holding the files, so the blocking
// processes are named when they can be found, and --force is suggested when it was not
// already used.
func extensionRemovalError(ctx context.Context, extensionDir string, forced bool, cause error) error {
	running, err := processutil.FindByExecutableDir(ctx, extensionDir)
	if err != nil || len(running) == 0 {
		return fmt.Errorf("failed to remove extension: %w", cause)
	}

	if forced {
		return &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("failed to remove extension, still in use by %s: %w",
				processutil.Describe(running), cause),
			Suggestion: "Stop the listed processes and run the command again.",
		}
	}

	return &internal.ErrorWithSuggestion{
		Err: fmt.Errorf("failed to remove extension, in use by %s: %w",
			processutil.Describe(running), cause),
		Suggestion: "Re-run with --force to stop those processes automatically, " +
			"or stop them yourself and try again.",
	}
}
