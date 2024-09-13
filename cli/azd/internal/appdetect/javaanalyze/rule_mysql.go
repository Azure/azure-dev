package javaanalyze

type ruleMysql struct {
}

func (mr *ruleMysql) Match(mavenProject *MavenProject) bool {
	if mavenProject.Dependencies != nil {
		for _, dep := range mavenProject.Dependencies {
			if dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-j" {
				return true
			}
		}
	}
	return false
}

func (mr *ruleMysql) Apply(javaProject *JavaProject) {
	javaProject.Resources = append(javaProject.Resources, Resource{
		Name: "MySQL",
		Type: "MySQL",
	})

	javaProject.ServiceBindings = append(javaProject.ServiceBindings, ServiceBinding{
		Name:     "MySQL",
		AuthType: AuthType_SYSTEM_MANAGED_IDENTITY,
	})
}
