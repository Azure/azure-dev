package javaanalyze

type ruleMongo struct {
}

func (mr *ruleMongo) match(javaProject *javaProject) bool {
	if javaProject.mavenProject.Dependencies != nil {
		for _, dep := range javaProject.mavenProject.Dependencies {
			if dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-mongodb" {
				return true
			}
			if dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-mongodb-reactive" {
				return true
			}
		}
	}
	return false
}

func (mr *ruleMongo) apply(azureYaml *AzureYaml) {
	azureYaml.Resources = append(azureYaml.Resources, &Resource{
		Name: "MongoDB",
		Type: "MongoDB",
	})

	azureYaml.ServiceBindings = append(azureYaml.ServiceBindings, ServiceBinding{
		Name:     "MongoDB",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
