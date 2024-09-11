package javaanalyze

type rule interface {
	Match(*MavenProject) bool
	Apply(*JavaProject)
}

func ApplyRules(mavenProject *MavenProject, rules []rule) (*JavaProject, error) {
	javaProject := &JavaProject{}

	for _, r := range rules {
		if r.Match(mavenProject) {
			r.Apply(javaProject)
		}
	}
	return javaProject, nil
}
