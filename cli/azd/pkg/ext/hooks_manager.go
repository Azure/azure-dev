package ext

import (
	"fmt"
	"os"
	"runtime"
	"strings"
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
	validHookNames := map[string]struct{}{}

	for _, commandName := range commands {
		// Convert things like `azd env list` => 'envlist`
		commandName = strings.TrimPrefix(commandName, "azd")
		commandName = strings.TrimSpace(commandName)
		commandName = strings.ReplaceAll(commandName, " ", "")
		commandName = strings.ToLower(string(prefix) + commandName)
		validHookNames[commandName] = struct{}{}
	}

	predicate := func(scriptName string, hookConfig *HookConfig) bool {
		_, has := validHookNames[scriptName]
		return has
	}

	return h.filterConfigs(hooks, predicate)
}

// Filters the specified hook configurations based on the predicate
// Will return an error if any configuration errors are found
func (h *HooksManager) filterConfigs(
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
