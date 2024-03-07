package contracts

type VsServerResult struct {
	Port int `json:"port"`
	Pid  int `json:"pid"`
	VersionResult
}
