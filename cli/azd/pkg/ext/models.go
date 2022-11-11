package ext

type ScriptType string
type ScriptLocation string

const (
	ScriptTypeBash       ScriptType     = "bash"
	ScriptTypePowershell ScriptType     = "powershell"
	ScriptLocationInline ScriptLocation = "inline"
	ScriptLocationPath   ScriptLocation = "path"
)

type ScriptConfig struct {
	// Internal name of the hook running for a given command
	Name string `yaml:",omitempty"`
	// The type of script hook (bash or powershell)
	Type ScriptType `yaml:"type,omitempty"`
	// The location of the script hook (file path or inline)
	Location ScriptLocation `yaml:"location,omitempty"`
	// When location is `path` a file path must be specified relative to the project or service
	Path string `yaml:"path,omitempty"`
	// When location is `inline` a script must be defined inline
	Script string `yaml:"script,omitempty"`
	// When set to true will not halt command execution even when a script error occurs.
	ContinueOnError bool `yaml:"continueOnError,omitempty"`
	// When set to true will bind the stdin, stdout & stderr to the running console
	Interactive bool `yaml:"interactive,omitempty"`
	// When running on windows use this override config
	Windows *ScriptConfig `yaml:"windows,omitempty"`
	// When running on linux/macos use this override config
	Linux *ScriptConfig `yaml:"linux,omitempty"`
}
