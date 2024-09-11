package javaanalyze

type Rule struct {
	Match func(MavenProject) bool
	Apply func(*JavaProject)
}

func matchesRule(mavenProject MavenProject, rule Rule) bool {
	return rule.Match(mavenProject)
}

func applyOperation(javaProject *JavaProject, rule Rule) {
	rule.Apply(javaProject)
}

func ApplyRules(mavenProject MavenProject, rules []Rule) error {
	javaProject := &JavaProject{}

	for _, rule := range rules {
		if matchesRule(mavenProject, rule) {
			applyOperation(javaProject, rule)
		}
	}
	return nil
}
