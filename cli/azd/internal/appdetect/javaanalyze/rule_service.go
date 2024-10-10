package javaanalyze

type ruleService struct {
	javaProject *javaProject
}

func (r *ruleService) match(javaProject *javaProject) bool {
	r.javaProject = javaProject
	return true
}

func (r *ruleService) apply(azureYaml *AzureYaml) {
	if azureYaml.Service == nil {
		azureYaml.Service = &Service{}
	}
	azureYaml.Service.Path = r.javaProject.mavenProject.path
}
