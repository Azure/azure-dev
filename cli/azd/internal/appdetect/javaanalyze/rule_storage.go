package javaanalyze

type ruleStorage struct {
}

func (mr *ruleStorage) Match(mavenProject *MavenProject) bool {
	if mavenProject.Dependencies != nil {
		for _, dep := range mavenProject.Dependencies {
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

func (mr *ruleStorage) Apply(javaProject *JavaProject) {
	javaProject.Resources = append(javaProject.Resources, Resource{
		Name: "Azure Storage",
		Type: "Azure Storage",
	})

	javaProject.ServiceBindings = append(javaProject.ServiceBindings, ServiceBinding{
		Name:     "Azure Storage",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
