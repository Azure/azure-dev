package javaanalyze

type ruleService struct {
	MavenProject *MavenProject
}

func (mr *ruleService) Match(mavenProject *MavenProject) bool {
	mr.MavenProject = mavenProject
	return true
}

func (mr *ruleService) Apply(javaProject *JavaProject) {
	if javaProject.Service == nil {
		javaProject.Service = &Service{}
	}
	javaProject.Service.Path = mr.MavenProject.Path
}
