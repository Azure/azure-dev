package javaanalyze

type rulePostgresql struct {
}

func (mr *rulePostgresql) match(javaProject *javaProject) bool {
	if javaProject.mavenProject.Dependencies != nil {
		for _, dep := range javaProject.mavenProject.Dependencies {
			if dep.GroupId == "org.postgresql" && dep.ArtifactId == "postgresql" {
				return true
			}
		}
	}
	return false
}

func (mr *rulePostgresql) apply(azureYaml *AzureYaml) {
	azureYaml.Resources = append(azureYaml.Resources, &Resource{
		Name: "PostgreSQL",
		Type: "PostgreSQL",
	})

	azureYaml.ServiceBindings = append(azureYaml.ServiceBindings, ServiceBinding{
		Name:     "PostgreSQL",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
