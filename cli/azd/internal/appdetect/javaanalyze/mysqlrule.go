package javaanalyze

type mysqlRule struct {
}

func (mr *mysqlRule) Match(mavenProject *MavenProject) bool {
	if mavenProject.Dependencies != nil {
		for _, dep := range mavenProject.Dependencies {
			if dep.GroupId == "com.mysql" && dep.ArtifactId == "mysql-connector-j" {
				return true
			}
		}
	}
	return false
}

func (mr *mysqlRule) Apply(javaProject *JavaProject) {
	javaProject.Resources = append(javaProject.Resources, Resource{
		Name: "MySQL",
		Type: "MySQL",
	})
}
