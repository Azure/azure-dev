package appdetect

type mavenProject struct {
	pom pom
}

func toMavenProject(pomFilePath string) (*mavenProject, error) {
	pom, err := toPom(pomFilePath)
	if err != nil {
		return nil, err
	}
	return &mavenProject{
		pom: *pom,
	}, nil
}
