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

type HookFilterPredicateFn func(scriptName string, hookConfig *HookConfig) bool

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

// Gets an array of all hook configurations
// Will return an error if any configuration errors are found
func (h *HooksManager) GetAll(hooks map[string]*HookConfig) ([]*HookConfig, error) {
	return h.filterConfigs(hooks, nil)
}

// Gets an array of hook configurations matching the specified hook type and commands
// Will return an error if any configuration errors are found
func (h *HooksManager) GetByParams(
	hooks map[string]*HookConfig,
	prefix HookType,
	commands ...string,
) ([]*HookConfig, error) {
	validHookNames := []string{}

	for _, commandName := range commands {
		// Convert things like `azd infra create` => 'configlist`
		commandName = strings.TrimPrefix(commandName, "azd")
		commandName = strings.TrimSpace(commandName)
		commandName = strings.ReplaceAll(commandName, " ", "")

		validHookNames = append(validHookNames, strings.ToLower(string(prefix)+commandName))
	}

	predicate := func(scriptName string, hookConfig *HookConfig) bool {
		index := slices.IndexFunc(validHookNames, func(hookName string) bool {
			return hookName == scriptName
		})

		return index > -1
	}

	return h.filterConfigs(hooks, predicate)
}

// Filters the specified hook configurations based on the predicate
// Will return an error if any configuration errors are found
func (h *HooksManager) filterConfigs(
	hooks map[string]*HookConfig,
	predicate HookFilterPredicateFn,
) ([]*HookConfig, error) {
	allHooks := []*HookConfig{}
	explicitHooks, err := h.getExplicitHooks(hooks, predicate)
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
		index := slices.IndexFunc(allHooks, func(hook *HookConfig) bool {
			return implicitHook.Name == hook.Name
		})

		if index >= 0 {
			log.Printf(
				"Skipping hook @ '%s'. An explicit hook for '%s' was already defined in azure.yaml.\n",
				implicitHook.path,
				implicitHook.Name,
			)
			continue
		}

		allHooks = append(allHooks, implicitHook)
	}

	return allHooks, nil
}

func (h *HooksManager) getExplicitHooks(
	hooks map[string]*HookConfig,
	predicate HookFilterPredicateFn,
) ([]*HookConfig, error) {
	matchingHooks := []*HookConfig{}

	// Find explicitly configured hooks from azure.yaml
	for scriptName, hookConfig := range hooks {
		if hookConfig == nil {
			continue
		}

		if predicate != nil && !predicate(scriptName, hookConfig) {
			continue
		}

		// If the hook config includes an OS specific configuration use that instead
		if runtime.GOOS == "windows" && hookConfig.Windows != nil {
			hookConfig = hookConfig.Windows
		} else if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && hookConfig.Posix != nil {
			hookConfig = hookConfig.Posix
		}

		hookConfig.Name = scriptName
		hookConfig.cwd = h.cwd

		if err := hookConfig.validate(); err != nil {
			return nil, fmt.Errorf("hook configuration for '%s' is invalid, %w", scriptName, err)
		}

		matchingHooks = append(matchingHooks, hookConfig)
	}

	return matchingHooks, nil
}

func (h *HooksManager) getImplicitHooks(predicate HookFilterPredicateFn) ([]*HookConfig, error) {
	matchingHooks := []*HookConfig{}

	hooksDir := filepath.Join(h.cwd, ".azure", "hooks")
	files, err := os.ReadDir(hooksDir)
	if err != nil {
		// Most common error would be `ErrNotExist`.
		// Log error for other error conditions (Permissions, etc)
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("failed to read azd hooks directory: %s", err.Error())
		}

		return matchingHooks, nil
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

		hookConfig := &HookConfig{
			Name: fileNameWithoutExt,
			cwd:  h.cwd,
			Run:  relativePath,
		}

		if predicate != nil && !predicate(fileNameWithoutExt, hookConfig) {
			continue
		}

		if err := hookConfig.validate(); err != nil {
			return nil, fmt.Errorf("hook configuration for '%s' is invalid, %w", fileNameWithoutExt, err)
		}

		matchingHooks = append(matchingHooks, hookConfig)
	}

	return matchingHooks, nil
}
