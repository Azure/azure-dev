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
	Name     string         `yaml:"omitempty"`
	Type     ScriptType     `yaml:"type,omitempty"`
	Location ScriptLocation `yaml:"location,omitempty"`
	Path     string         `yaml:"path,omitempty"`
	Script   string         `yaml:"script,omitempty"`
}
