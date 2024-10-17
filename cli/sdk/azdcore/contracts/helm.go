package contracts

type HelmConfig struct {
	Repositories []*HelmRepository `yaml:"repositories"`
	Releases     []*HelmRelease    `yaml:"releases"`
}

type HelmRepository struct {
	Name string `yaml:"name"`
	Url  string `yaml:"url"`
}

type HelmRelease struct {
	Name      string `yaml:"name"`
	Chart     string `yaml:"chart"`
	Version   string `yaml:"version"`
	Namespace string `yaml:"namespace"`
	Values    string `yaml:"values"`
}
