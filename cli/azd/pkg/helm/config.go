package helm

type Config struct {
	Repositories []*Repository `yaml:"repositories"`
	Releases     []*Release    `yaml:"releases"`
}

type Repository struct {
	Name string `yaml:"name"`
	Url  string `yaml:"url"`
}

type Release struct {
	Name      string `yaml:"name"`
	Chart     string `yaml:"chart"`
	Version   string `yaml:"version"`
	Namespace string `yaml:"namespace"`
	Values    string `yaml:"values"`
}
