// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
)

// HookType represents the execution timing of a hook relative to the
// associated command. Supported values are 'pre' and 'post'.
type HookType string

// HookPlatformType identifies the operating system platform for
// platform-specific hook overrides.
type HookPlatformType string

const (
	// Executes pre hooks
	HookTypePre HookType = "pre"
	// Execute post hooks
	HookTypePost        HookType         = "post"
	HookTypeNone        HookType         = ""
	HookPlatformWindows HookPlatformType = "windows"
	HookPlatformPosix   HookPlatformType = "posix"
)

var (
	// ErrScriptTypeUnknown indicates the hook kind could not be inferred
	// from the script path and was not explicitly configured.
	ErrScriptTypeUnknown error = errors.New(
		"unable to determine hook kind. " +
			"Ensure 'kind' or 'shell' is set in your hook configuration, " +
			"or use a file with a recognized extension " +
			"(.sh, .ps1, .py, .js, .ts, .cs)",
	)
	// ErrRunRequired indicates the hook configuration is missing the mandatory 'run' field.
	ErrRunRequired error = errors.New(
		"'run' is required for every hook configuration. " +
			"Set 'run' to an inline script or a relative file path",
	)
	// ErrUnsupportedScriptType indicates the script file has an extension
	// that is not recognized and no explicit kind or shell was set.
	ErrUnsupportedScriptType error = errors.New(
		"script type is not valid. " +
			"Supported extensions: .sh, .ps1, .py, .js, .ts, .cs. " +
			"Alternatively, set 'kind' (e.g. kind: python) " +
			"or 'shell' (e.g. shell: sh)",
	)
)

// Generic action function that may return an error
type InvokeFn func() error

// HookConfig defines the configuration for a single hook in azure.yaml.
// Hooks are lifecycle scripts that run before or after azd commands.
// Every hook is executed through a [tools.HookExecutor] resolved via IoC
// based on the hook's Kind — including Bash, PowerShell, Python,
// and future executor types (JS, TS, DotNet).
type HookConfig struct {
	// relativeScriptPath holds the file path relative to the
	// project or service directory. Set by parseRunTarget when
	// Run references an existing file. Empty for inline scripts.
	relativeScriptPath string
	// Stores a value whether or not this hook config has been previously validated
	validated bool
	// inputCwd is the working directory injected by the hooks
	// manager (e.g. the service directory for service hooks).
	// Distinguished from resolvedDir, which is the final
	// absolute working directory computed during validate().
	inputCwd string
	// projectDir is the project root directory (where azure.yaml
	// lives). Used as the security boundary for path containment
	// checks. When empty, inputCwd is used as the boundary.
	projectDir string
	// inlineScript holds the inline script content to execute.
	// Set by parseRunTarget when Run does not reference an
	// existing file. Empty for file-based hooks.
	inlineScript string
	// Indicates if the shell was automatically detected based on OS (used for warnings)
	usingDefaultShell bool
	// resolvedScriptPath is the absolute path to the script file,
	// computed during validate(). Empty for inline scripts.
	resolvedScriptPath string
	// resolvedDir is the absolute working directory for hook
	// execution, computed during validate().
	resolvedDir string

	// Internal name of the hook running for a given command
	Name string `yaml:",omitempty"`
	// Kind specifies the executor type of the hook script.
	// Allowed values: "sh", "pwsh", "js", "ts", "python", "dotnet".
	// When empty, the kind is auto-detected from the file extension
	// of the run path (e.g. .py → python, .ps1 → pwsh). If Kind
	// and Shell are both empty and run references a file,
	// the extension is used. For inline scripts, Kind or Shell
	// must be set explicitly.
	Kind language.HookKind `yaml:"kind,omitempty" json:"kind,omitempty"`
	// Shell is a deprecated alias for Kind. When set in YAML
	// (shell: sh) it is mapped to Kind during validate().
	// Retained for backwards compatibility with legacy configs.
	Shell string `yaml:"shell,omitempty" json:"-"`
	// Dir specifies the working directory for hook execution,
	// used as the project context for dependency installation and builds.
	// When empty, defaults to the directory containing the script
	// referenced by the run field. Only set this when the project root
	// differs from the script's directory.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
	// The inline script to execute or path to existing file
	Run string `yaml:"run,omitempty"`
	// When set to true will not halt command execution even when a script error occurs.
	ContinueOnError bool `yaml:"continueOnError,omitempty"`
	// When set to true will bind the stdin, stdout & stderr to the running console
	Interactive bool `yaml:"interactive,omitempty"`
	// When running on windows use this override config
	Windows *HookConfig `yaml:"windows,omitempty"`
	// When running on linux/macos use this override config
	Posix *HookConfig `yaml:"posix,omitempty"`
	// Environment variables in this list are added to the hook script and if the value is a akvs:// reference
	// it will be resolved to the secret value
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

// validate normalizes and validates the hook configuration. It resolves
// the script location (inline vs. file path) and ensures that Kind
// is always resolved to a concrete [language.HookKind] value.
//
// Kind resolution priority:
//  1. Explicit Kind field
//  2. Explicit Shell field (deprecated alias, mapped to Kind)
//  3. File extension via [language.InferKindFromPath]
//  4. OS default (Bash on Unix, PowerShell on Windows) for inline scripts
//
// After a successful call, Kind is the single source of truth for
// executor selection and the hook is ready for execution.
func (hc *HookConfig) validate() error {
	if hc.validated {
		return nil
	}

	if hc.Run == "" {
		return ErrRunRequired
	}

	dirExplicit, err := hc.parseRunTarget()
	if err != nil {
		return err
	}
	if err := hc.resolveKind(dirExplicit); err != nil {
		return err
	}
	if err := hc.resolvePaths(dirExplicit); err != nil {
		return err
	}
	if err := hc.enforceContainment(); err != nil {
		return err
	}

	hc.validated = true
	return nil
}

// parseRunTarget normalizes the Run field and determines whether it
// references an existing file or an inline script. It sets
// relativeScriptPath (for file-based hooks) or inlineScript (for
// inline hooks) and returns whether Dir was explicitly provided.
func (hc *HookConfig) parseRunTarget() (bool, error) {
	// Record whether Dir was explicitly provided by the user
	// before any auto-inference fills it in.
	dirExplicit := hc.Dir != ""

	relativeCheckPath := strings.ReplaceAll(
		hc.Run, "/", string(os.PathSeparator),
	)

	// Resolve the full path for the os.Stat check. When Dir is
	// explicitly set and run is relative, we try Dir first and
	// fall back to inputCwd (project root). This supports both:
	//   run: main.py        + dir: hooks/preprovision
	//   run: hooks/deploy.py + dir: custom_workdir
	fullCheckPath := relativeCheckPath
	foundViaDir := false
	if hc.inputCwd != "" {
		if filepath.IsAbs(relativeCheckPath) {
			fullCheckPath = relativeCheckPath
		} else if dirExplicit {
			dir := hc.Dir
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(hc.inputCwd, dir)
			}
			dirPath := filepath.Join(
				dir, relativeCheckPath,
			)
			info, dirErr := os.Stat(dirPath)
			if dirErr == nil && !info.IsDir() {
				fullCheckPath = dirPath
				foundViaDir = true
			} else {
				fullCheckPath = filepath.Join(
					hc.inputCwd, relativeCheckPath,
				)
			}
		} else {
			fullCheckPath = filepath.Join(
				hc.inputCwd, relativeCheckPath,
			)
		}
	}

	stats, err := os.Stat(fullCheckPath)
	if err == nil && !stats.IsDir() {
		hc.relativeScriptPath = relativeCheckPath
		if foundViaDir {
			dirExplicit = true
		}
	} else {
		hc.inlineScript = hc.Run
		dirExplicit = false
	}

	return dirExplicit, nil
}

// resolveKind resolves the hook's Kind field from the explicit Kind
// or Shell fields, the file extension, or the OS default shell.
// It also infers Dir for non-shell file hooks when Dir was not
// explicitly set by the user.
func (hc *HookConfig) resolveKind(dirExplicit bool) error {
	// Kind resolution — priority:
	// 1. explicit Kind  2. Shell alias
	// 3. file extension  4. OS default for inline scripts
	if hc.Kind == language.HookKindUnknown && hc.Shell != "" {
		hc.Kind = language.HookKind(hc.Shell)
	}
	if hc.Kind == language.HookKindUnknown &&
		hc.relativeScriptPath != "" {
		hc.Kind = language.InferKindFromPath(
			hc.relativeScriptPath,
		)
	}

	// Reject inline scripts for non-shell executors. Only Bash
	// and PowerShell executors support inline script content;
	// other executors require a file on disk.
	if hc.inlineScript != "" &&
		hc.Kind != language.HookKindUnknown &&
		!hc.Kind.IsShell() {
		return fmt.Errorf(
			"inline scripts are not supported for %s hooks. "+
				"Write your script to a file and set 'run' "+
				"to the file path "+
				"(e.g. run: ./hooks/my-script.py)",
			hc.Kind,
		)
	}

	// Non-shell executors handle their own runtime and dependency
	// setup; no shell type resolution or temp script is needed.
	if hc.Kind != language.HookKindUnknown &&
		!hc.Kind.IsShell() {
		// Auto-infer Dir from the script's directory when not
		// explicitly set by the user.
		if !dirExplicit && hc.relativeScriptPath != "" {
			hc.Dir = filepath.Dir(hc.relativeScriptPath)
		}
	} else {
		// For inline scripts with no resolved kind, default to
		// the OS-appropriate shell kind.
		if hc.Kind == language.HookKindUnknown &&
			hc.inlineScript != "" {
			hc.Kind = defaultKindForOS()
			hc.usingDefaultShell = true
		}

		// For file-based hooks with an unrecognized extension.
		if hc.Kind == language.HookKindUnknown {
			return fmt.Errorf(
				"script with file extension '%s' is not "+
					"valid. %w.",
				filepath.Ext(hc.relativeScriptPath),
				ErrUnsupportedScriptType,
			)
		}
	}

	return nil
}

// resolvePaths computes absolute resolvedDir and resolvedScriptPath
// from the hook's Dir, relativeScriptPath, and inputCwd fields.
// After this method, resolvedDir and resolvedScriptPath are the
// single source of truth for hook execution paths.
func (hc *HookConfig) resolvePaths(dirExplicit bool) error {
	// cwdDir is the working directory for resolving relative
	// paths (e.g. the service directory for service hooks).
	cwdDir := hc.inputCwd
	if cwdDir == "" {
		var err error
		cwdDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf(
				"resolving working directory: %w", err,
			)
		}
	}

	// Resolve working directory.
	if hc.Dir != "" {
		dir := hc.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwdDir, dir)
		}
		hc.resolvedDir = dir
	} else if hc.relativeScriptPath != "" &&
		!hc.Kind.IsShell() {
		// Non-shell hooks: derive cwd from script directory so
		// project-relative tooling (venvs, node_modules) works.
		hc.resolvedDir = filepath.Dir(
			filepath.Join(cwdDir, hc.relativeScriptPath),
		)
	} else {
		hc.resolvedDir = cwdDir
	}

	// Resolve script path to absolute.
	if hc.relativeScriptPath != "" {
		if dirExplicit &&
			!filepath.IsAbs(hc.relativeScriptPath) {
			// Dir was explicitly set — run path is relative
			// to Dir, not cwdDir alone.
			dir := hc.Dir
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwdDir, dir)
			}
			hc.resolvedScriptPath = filepath.Join(
				dir, hc.relativeScriptPath,
			)
		} else if !filepath.IsAbs(hc.relativeScriptPath) {
			hc.resolvedScriptPath = filepath.Join(
				cwdDir, hc.relativeScriptPath,
			)
		} else {
			hc.resolvedScriptPath = hc.relativeScriptPath
		}
	}

	return nil
}

// enforceContainment validates that file-based hooks do not escape
// the project root via ".." in Dir or Run. Inline hooks are exempt
// because they are saved to an OS temp file for execution.
func (hc *HookConfig) enforceContainment() error {
	if hc.relativeScriptPath == "" {
		// Inline hooks: resolve to absolute path but skip
		// containment validation.
		absDir, absErr := filepath.Abs(hc.resolvedDir)
		if absErr != nil {
			return fmt.Errorf(
				"resolving hook directory: %w", absErr,
			)
		}
		hc.resolvedDir = absDir
		return nil
	}

	// boundaryDir is the project root used for path
	// containment validation. For service-level hooks this
	// is the project root (where azure.yaml lives), which
	// may differ from inputCwd (the service directory).
	boundaryDir := hc.projectDir
	if boundaryDir == "" {
		boundaryDir = hc.inputCwd
	}
	if boundaryDir == "" {
		var err error
		boundaryDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf(
				"resolving working directory: %w", err,
			)
		}
	}

	// Validate that resolvedDir stays within the project
	// root to prevent path traversal via ".." in Dir.
	absDir, absErr := filepath.Abs(hc.resolvedDir)
	if absErr != nil {
		return fmt.Errorf(
			"resolving hook directory: %w", absErr,
		)
	}
	absBoundary, absErr := filepath.Abs(boundaryDir)
	if absErr != nil {
		return fmt.Errorf(
			"resolving boundary directory: %w",
			absErr,
		)
	}
	if !osutil.IsPathContained(absBoundary, absDir) {
		return fmt.Errorf(
			"hook directory %q escapes "+
				"project root %q",
			hc.Dir, boundaryDir,
		)
	}
	hc.resolvedDir = absDir

	// Validate that resolvedScriptPath stays within
	// the project root to prevent path traversal via
	// ".." in Run.
	if hc.resolvedScriptPath != "" {
		absScript, scriptErr := filepath.Abs(
			hc.resolvedScriptPath,
		)
		if scriptErr != nil {
			return fmt.Errorf(
				"resolving hook script path: %w",
				scriptErr,
			)
		}
		if !osutil.IsPathContained(
			absBoundary, absScript,
		) {
			return fmt.Errorf(
				"hook script path %q escapes "+
					"project root %q",
				hc.Run, boundaryDir,
			)
		}
		hc.resolvedScriptPath = absScript
	}

	return nil
}

// IsPowerShellHook determines if a hook configuration uses PowerShell.
// It checks the resolved Kind first, then falls back to the raw
// Shell field and file extension for hooks that have not yet been
// validated.
func (hc *HookConfig) IsPowerShellHook() bool {
	if hc.Kind == language.HookKindPowerShell {
		return true
	}

	// Check raw Shell field (pre-validation).
	if hc.Shell == "pwsh" {
		return true
	}

	// Check file extension for unvalidated hooks.
	if hc.Run != "" {
		if strings.HasSuffix(strings.ToLower(hc.Run), ".ps1") {
			return true
		}
	}

	// Check OS-specific hook configurations
	if runtime.GOOS == "windows" && hc.Windows != nil {
		return hc.Windows.IsPowerShellHook()
	} else if (runtime.GOOS == "linux" ||
		runtime.GOOS == "darwin") && hc.Posix != nil {
		return hc.Posix.IsPowerShellHook()
	}

	return false
}

// IsUsingDefaultShell returns true if the hook is using the OS default shell
// because no shell was explicitly configured
func (hc *HookConfig) IsUsingDefaultShell() bool {
	return hc.usingDefaultShell
}

// InferHookType extracts the hook timing prefix ("pre" or "post")
// from a hook name and returns the remaining command name. For
// example, "preprovision" → (HookTypePre, "provision").
func InferHookType(name string) (HookType, string) {
	// Validate name length so go doesn't PANIC for string slicing below
	if len(name) < 4 {
		return HookTypeNone, name
	} else if name[:3] == "pre" {
		return HookTypePre, name[3:]
	} else if name[:4] == "post" {
		return HookTypePost, name[4:]
	}

	return HookTypeNone, name
}

// HooksConfigSignature returns a stable signature for a set of hook configurations.
// The signature ignores runtime-only state and changes whenever the effective hook config changes.
func HooksConfigSignature(hooks map[string][]*HookConfig) string {
	if len(hooks) == 0 {
		return ""
	}

	var builder strings.Builder

	for _, hookName := range slices.Sorted(maps.Keys(hooks)) {
		builder.WriteString(hookName)
		builder.WriteByte('\x00')

		for index, hookConfig := range hooks[hookName] {
			builder.WriteString(strconv.Itoa(index))
			builder.WriteByte('\x00')
			appendHookConfigSignature(&builder, hookConfig)
		}
	}

	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func appendHookConfigSignature(builder *strings.Builder, hookConfig *HookConfig) {
	if hookConfig == nil {
		builder.WriteString("<nil>\x00")
		return
	}

	builder.WriteString(string(hookConfig.Kind))
	builder.WriteByte('\x00')
	builder.WriteString(hookConfig.Shell)
	builder.WriteByte('\x00')
	builder.WriteString(hookConfig.Run)
	builder.WriteByte('\x00')
	builder.WriteString(hookConfig.Dir)
	builder.WriteByte('\x00')
	builder.WriteString(strconv.FormatBool(hookConfig.ContinueOnError))
	builder.WriteByte('\x00')
	builder.WriteString(strconv.FormatBool(hookConfig.Interactive))
	builder.WriteByte('\x00')

	for _, secretName := range slices.Sorted(maps.Keys(hookConfig.Secrets)) {
		builder.WriteString(secretName)
		builder.WriteByte('=')
		builder.WriteString(hookConfig.Secrets[secretName])
		builder.WriteByte('\x00')
	}

	appendHookConfigSignature(builder, hookConfig.Windows)
	appendHookConfigSignature(builder, hookConfig.Posix)
}

// defaultKindForOS returns the default shell kind for the
// current operating system: PowerShell on Windows, Bash elsewhere.
func defaultKindForOS() language.HookKind {
	if runtime.GOOS == "windows" {
		return language.HookKindPowerShell
	}
	return language.HookKindBash
}
