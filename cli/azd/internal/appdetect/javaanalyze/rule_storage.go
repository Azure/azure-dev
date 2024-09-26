package javaanalyze

type ruleStorage struct {
}

func (r *ruleStorage) match(javaProject *javaProject) bool {
	if javaProject.mavenProject.Dependencies != nil {
		for _, dep := range javaProject.mavenProject.Dependencies {
			if dep.GroupId == "com.azure" && dep.ArtifactId == "" {
				return true
			}
			if dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-storage" {
				return true
			}
			if dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-storage-blob" {
				return true
			}
			if dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-storage-file-share" {
				return true
			}
			if dep.GroupId == "com.azure.spring" && dep.ArtifactId == "spring-cloud-azure-starter-storage-queue" {
				return true
			}
		}
	}
	return false
}

func (r *ruleStorage) apply(azureYaml *AzureYaml) {
	azureYaml.Resources = append(azureYaml.Resources, &Resource{
		Name: "Azure Storage",
		Type: "Azure Storage",
	})

	azureYaml.ServiceBindings = append(azureYaml.ServiceBindings, ServiceBinding{
		Name:     "Azure Storage",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
