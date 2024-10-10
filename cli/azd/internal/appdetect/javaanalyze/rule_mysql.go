package javaanalyze

type ruleMysql struct {
}

func (mr *ruleMysql) match(javaProject *javaProject) bool {
	if javaProject.mavenProject.Dependencies != nil {
		for _, dep := range javaProject.mavenProject.Dependencies {
			if dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-j" {
				return true
			}
		}
	}
	return false
}

func (mr *ruleMysql) apply(azureYaml *AzureYaml) {
	azureYaml.Resources = append(azureYaml.Resources, &Resource{
		Name: "MySQL",
		Type: "MySQL",
	})

	azureYaml.ServiceBindings = append(azureYaml.ServiceBindings, ServiceBinding{
		Name:     "MySQL",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
