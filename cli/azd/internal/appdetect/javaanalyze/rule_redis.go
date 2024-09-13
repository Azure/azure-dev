package javaanalyze

type ruleRedis struct {
}

func (mr *ruleRedis) Match(mavenProject *MavenProject) bool {

	return false
}

func (mr *ruleRedis) Apply(javaProject *JavaProject) {
	javaProject.Resources = append(javaProject.Resources, Resource{
		Name: "Redis",
		Type: "Redis",
	})
}
