package contracts

import "fmt"

// The type of hooks. Supported values are 'pre' and 'post'
type HookType string
type HookPlatformType string
type ShellType string
type ScriptLocation string

const (
	ShellTypeBash         ShellType      = "sh"
	ShellTypePowershell   ShellType      = "pwsh"
	ScriptTypeUnknown     ShellType      = ""
	ScriptLocationInline  ScriptLocation = "inline"
	ScriptLocationPath    ScriptLocation = "path"
	ScriptLocationUnknown ScriptLocation = ""
	// Executes pre hooks
	HookTypePre HookType = "pre"
	// Execute post hooks
	HookTypePost        HookType         = "post"
	HookTypeNone        HookType         = ""
	HookPlatformWindows HookPlatformType = "windows"
	HookPlatformPosix   HookPlatformType = "posix"
)

type HookConfig struct {
	// The location of the script hook (file path or inline)
	location ScriptLocation
	// When location is `path` a file path must be specified relative to the project or service
	path string
	// Stores a value whether or not this hook config has been previously validated
	validated bool
	// Stores the working directory set for this hook config
	cwd string
	// When location is `inline` a script must be defined inline
	script string

	// Internal name of the hook running for a given command
	Name string `yaml:",omitempty"`
	// The type of script hook (bash or powershell)
	Shell ShellType `yaml:"shell,omitempty"`
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
}

// HooksConfig is an alias for map of hook names to slice of hook configurations
// This custom alias type is used to help support YAML unmarshalling of legacy single hook configurations
// and new multiple hook configurations
type HooksConfig map[string][]*HookConfig

// UnmarshalYAML converts the hooks configuration from YAML supporting both legacy single hook configurations
// and new multiple hook configurations
func (ch *HooksConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var legacyConfig map[string]*HookConfig

	// Attempt to unmarshal the legacy single hook configuration
	if err := unmarshal(&legacyConfig); err == nil {
		newConfig := HooksConfig{}

		for key, value := range legacyConfig {
			newConfig[key] = []*HookConfig{value}
		}

		*ch = newConfig
	} else { // Unmarshal the new multiple hook configuration
		var newConfig map[string][]*HookConfig
		if err := unmarshal(&newConfig); err != nil {
			return fmt.Errorf("failed to unmarshal hooks configuration: %w", err)
		}

		*ch = newConfig
	}

	return nil
}

// MarshalYAML marshals the hooks configuration to YAML supporting both legacy single hook configurations
func (ch HooksConfig) MarshalYAML() (interface{}, error) {
	if len(ch) == 0 {
		return nil, nil
	}

	result := map[string]any{}
	for key, hooks := range ch {
		if len(hooks) == 1 {
			result[key] = hooks[0]
		} else {
			result[key] = hooks
		}
	}

	return result, nil
}
