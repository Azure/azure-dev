package contracts

type EnvListEnvironment struct {
	Name       string `json:"name"`
	IsDefault  bool   `json:"isDefault"`
	DotEnvPath string `json:"dotEnvPath"`
}
