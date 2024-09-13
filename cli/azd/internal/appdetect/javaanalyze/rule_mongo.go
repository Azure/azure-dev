package javaanalyze

type ruleMongo struct {
}

func (mr *ruleMongo) Match(mavenProject *MavenProject) bool {
	if mavenProject.Dependencies != nil {
		for _, dep := range mavenProject.Dependencies {
			if dep.GroupId == "org.springframework.boot" && dep.ArtifactId == "spring-boot-starter-data-mongodb" {
				return true
			}
		}
	}
	return false
}

func (mr *ruleMongo) Apply(javaProject *JavaProject) {
	javaProject.Resources = append(javaProject.Resources, Resource{
		Name: "MongoDB",
		Type: "MongoDB",
	})

	javaProject.ServiceBindings = append(javaProject.ServiceBindings, ServiceBinding{
		Name:     "MongoDB",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
