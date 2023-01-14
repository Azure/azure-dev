package ext

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/exp/slices"
)

type HookFilterPredicateFn func(scriptName string, scriptConfig *ScriptConfig) bool

// Hooks enable support to invoke integration scripts before & after commands
// Scripts can be invoked at the project or service level or
type HooksManager struct {
	cwd string
}

// NewHooks creates a new instance of CommandHooks
// When `cwd` is empty defaults to current shell working directory
func NewHooksManager(
	cwd string,
) *HooksManager {
	if cwd == "" {
		osWd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		cwd = osWd
	}

	return &HooksManager{
		cwd: cwd,
	}
}

// Gets an array of all script configurations
// Will return an error if any configuration errors are found
func (h *HooksManager) GetAllScriptConfigs(scripts map[string]*ScriptConfig) ([]*ScriptConfig, error) {
	return h.filterScriptConfigs(scripts, nil)
}

// Gets an array of script configurations matching the specified hook type and commands
// Will return an error if any configuration errors are found
func (h *HooksManager) GetScriptConfigsForHook(
	scripts map[string]*ScriptConfig,
	prefix HookType,
	commands ...string,
) ([]*ScriptConfig, error) {
	validHookNames := []string{}

	for _, commandName := range commands {
		// Convert things like `azd infra create` => 'configlist`
		commandName = strings.TrimPrefix(commandName, "azd")
		commandName = strings.TrimSpace(commandName)
		commandName = strings.ReplaceAll(commandName, " ", "")

		validHookNames = append(validHookNames, strings.ToLower(string(prefix)+commandName))
	}

	predicate := func(scriptName string, scriptConfig *ScriptConfig) bool {
		index := slices.IndexFunc(validHookNames, func(hookName string) bool {
			return hookName == scriptName
		})

		return index > -1
	}

	return h.filterScriptConfigs(scripts, predicate)
}

// Filters the specified script configurations based on the predicate
// Will return an error if any configuration errors are found
func (h *HooksManager) filterScriptConfigs(
	scripts map[string]*ScriptConfig,
	predicate HookFilterPredicateFn,
) ([]*ScriptConfig, error) {
	allHooks := []*ScriptConfig{}
	explicitHooks, err := h.getExplicitHooks(scripts, predicate)
	if err != nil {
		return nil, err
	}

	implicitHooks, err := h.getImplicitHooks(predicate)
	if err != nil {
		return nil, err
	}

	allHooks = append(allHooks, explicitHooks...)

	// Only append implicit hooks that were not already wired up explicitly.
	for _, implicitHook := range implicitHooks {
		index := slices.IndexFunc(allHooks, func(hook *ScriptConfig) bool {
			return implicitHook.Name == hook.Name
		})

		if index >= 0 {
			log.Printf(
				"Skipping hook @ '%s'. An explicit hook for '%s' was already defined in azure.yaml.\n",
				implicitHook.Path,
				implicitHook.Name,
			)
			continue
		}

		allHooks = append(allHooks, implicitHook)
	}

	return allHooks, nil
}

func (h *HooksManager) getExplicitHooks(
	scripts map[string]*ScriptConfig,
	predicate HookFilterPredicateFn,
) ([]*ScriptConfig, error) {
	matchingScripts := []*ScriptConfig{}

	// Find explicitly configured hooks from azure.yaml
	for scriptName, scriptConfig := range scripts {
		if scriptConfig == nil {
			continue
		}

		if predicate != nil && !predicate(scriptName, scriptConfig) {
			continue
		}

		// If the script config includes an OS specific configuration use that instead
		if runtime.GOOS == "windows" && scriptConfig.Windows != nil {
			scriptConfig = scriptConfig.Windows
		} else if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && scriptConfig.Posix != nil {
			scriptConfig = scriptConfig.Posix
		}

		scriptConfig.Name = scriptName
		scriptConfig.Path = strings.ReplaceAll(scriptConfig.Path, "/", string(os.PathSeparator))

		if err := scriptConfig.validate(); err != nil {
			return nil, fmt.Errorf("hook configuration for '%s' is invalid, %w", scriptName, err)
		}

		matchingScripts = append(matchingScripts, scriptConfig)
	}

	return matchingScripts, nil
}

func (h *HooksManager) getImplicitHooks(predicate HookFilterPredicateFn) ([]*ScriptConfig, error) {
	matchingScripts := []*ScriptConfig{}

	hooksDir := filepath.Join(h.cwd, ".azure", "hooks")
	files, err := os.ReadDir(hooksDir)
	if err != nil {
		// Most common error would be `ErrNotExist`.
		// Log error for other error conditions (Permissions, etc)
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("failed to read azd hooks directory: %s", err.Error())
		}

		return matchingScripts, nil
	}

	// Find implicit / convention based hooks in the .azure/hooks directory
	// Find `predeploy.sh` or similar matching a hook prefix & valid command name
	for _, file := range files {
		fileName := file.Name()
		fileNameWithoutExt := strings.TrimSuffix(fileName, path.Ext(fileName))
		isDir := file.IsDir()

		if isDir {
			continue
		}

		relativePath, err := filepath.Rel(h.cwd, filepath.Join(hooksDir, fileName))
		if err != nil {
			// This err should never happen since we are only looking for files inside the specified cwd.
			log.Printf("Found script hook '%s', but is outside of project folder. Error: %s\n", file.Name(), err.Error())
			continue
		}

		scriptConfig := &ScriptConfig{
			Name: fileNameWithoutExt,
			Path: relativePath,
		}

		if predicate != nil && !predicate(fileNameWithoutExt, scriptConfig) {
			continue
		}

		if err := scriptConfig.validate(); err != nil {
			return nil, fmt.Errorf("hook configuration for '%s' is invalid, %w", fileNameWithoutExt, err)
		}

		matchingScripts = append(matchingScripts, scriptConfig)
	}

	return matchingScripts, nil
}
