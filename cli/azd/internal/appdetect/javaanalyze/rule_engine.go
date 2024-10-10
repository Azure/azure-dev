package javaanalyze

type rule interface {
	match(project *javaProject) bool
	apply(azureYaml *AzureYaml)
}

func applyRules(javaProject *javaProject, rules []rule) (*AzureYaml, error) {
	azureYaml := &AzureYaml{}

	for _, r := range rules {
		if r.match(javaProject) {
			r.apply(azureYaml)
		}
	}
	return azureYaml, nil
}
