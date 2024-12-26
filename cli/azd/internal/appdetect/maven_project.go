package appdetect

type mavenProject struct {
	pom pom
}

func createMavenProject(pomFilePath string) (mavenProject, error) {
	pom, err := createEffectivePomOrSimulatedEffectivePom(pomFilePath)
	if err != nil {
		return mavenProject{}, err
	}
	return mavenProject{
		pom: pom,
	}, nil
}
