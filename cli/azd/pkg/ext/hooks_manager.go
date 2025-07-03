// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"fmt"
	"log"
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
func (h *HooksManager) GetAll(hooks map[string][]*HookConfig) ([]*HookConfig, error) {
	return h.filterConfigs(hooks, nil)
}

// Gets an array of hook configurations matching the specified hook type and commands
// Will return an error if any configuration errors are found
func (h *HooksManager) GetByParams(
	hooks map[string][]*HookConfig,
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
	hooksMap map[string][]*HookConfig,
	predicate HookFilterPredicateFn,
) ([]*HookConfig, error) {
	matchingHooks := []*HookConfig{}

	// Find explicitly configured hooks from azure.yaml
	for scriptName, hooks := range hooksMap {
		if hooks == nil {
			continue
		}

		for _, hook := range hooks {
			if hook == nil {
				log.Printf("hook configuration for '%s' is missing", scriptName)
				continue
			}

			if predicate != nil && !predicate(scriptName, hook) {
				continue
			}

			// If the hook config includes an OS specific configuration use that instead
			// but preserve precedence for Interactive and ContinueOnError settings
			if runtime.GOOS == "windows" && hook.Windows != nil {
				hook = MergeHookConfig(hook, hook.Windows)
			} else if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && hook.Posix != nil {
				hook = MergeHookConfig(hook, hook.Posix)
			}

			hook.Name = scriptName
			hook.cwd = h.cwd

			if err := hook.validate(); err != nil {
				return nil, fmt.Errorf("hook configuration for '%s' is invalid, %w", scriptName, err)
			}

			matchingHooks = append(matchingHooks, hook)
		}
	}

	return matchingHooks, nil
}
