package contracts

type EnvListEnvironment struct {
	Name       string `json:"Name"`
	IsDefault  bool   `json:"IsDefault"`
	DotEnvPath string `json:"DotEnvPath"`
}
