package environment

type EnvironmentListItem struct {
	Name       string `json:"Name"`
	IsDefault  bool   `json:"IsDefault"`
	DotEnvPath string `json:"DotEnvPath"`
	ConfigPath string `json:"ConfigPath"`
}
