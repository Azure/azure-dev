package appdetect

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"os/exec"
	"strings"
)

func getMavenProjectOfEffectivePom(pomPath string) (mavenProject, error) {
	if !commandExistsInPath("java") {
		return mavenProject{}, fmt.Errorf("can not get effective pom because java command not exist")
	}
	mvn, err := getMvnCommand(pomPath)
	if err != nil {
		return mavenProject{}, err
	}
	cmd := exec.Command(mvn, "help:effective-pom", "-f", pomPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return mavenProject{}, err
	}
	effectivePom, err := getEffectivePomFromConsoleOutput(string(output))
	if err != nil {
		return mavenProject{}, err
	}
	var project mavenProject
	if err := xml.Unmarshal([]byte(effectivePom), &project); err != nil {
		return mavenProject{}, fmt.Errorf("parsing xml: %w", err)
	}
	return project, nil
}

func commandExistsInPath(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

func getEffectivePomFromConsoleOutput(consoleOutput string) (string, error) {
	var effectivePom strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(consoleOutput))
	inProject := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "<project") {
			inProject = true
		} else if strings.HasPrefix(strings.TrimSpace(line), "</project>") {
			effectivePom.WriteString(line)
			break
		}
		if inProject {
			effectivePom.WriteString(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan console output. %w", err)
	}
	return effectivePom.String(), nil
}
