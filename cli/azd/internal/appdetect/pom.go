package appdetect

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// pom represents the top-level structure of a Maven POM file.
type pom struct {
	XmlName              xml.Name             `xml:"project"`
	Parent               parent               `xml:"parent"`
	Modules              []string             `xml:"modules>module"` // Capture the modules
	Properties           Properties           `xml:"properties"`
	Dependencies         []dependency         `xml:"dependencies>dependency"`
	DependencyManagement dependencyManagement `xml:"dependencyManagement"`
	Build                build                `xml:"build"`
	path                 string
}

// Parent represents the parent POM if this project is a module.
type parent struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
}

type Properties struct {
	Entries []Property `xml:",any"` // Capture all elements inside <properties>
}

type Property struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

// Dependency represents a single Maven dependency.
type dependency struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope,omitempty"`
}

// DependencyManagement includes a list of dependencies that are managed.
type dependencyManagement struct {
	Dependencies []dependency `xml:"dependencies>dependency"`
}

// Build represents the build configuration which can contain plugins.
type build struct {
	Plugins []plugin `xml:"plugins>plugin"`
}

// Plugin represents a build plugin.
type plugin struct {
	GroupId    string `xml:"groupId"`
	ArtifactId string `xml:"artifactId"`
	Version    string `xml:"version"`
}

func toPom(filePath string) (*pom, error) {
	bytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var unmarshalledPom pom
	if err := xml.Unmarshal(bytes, &unmarshalledPom); err != nil {
		return nil, fmt.Errorf("parsing xml: %w", err)
	}

	// replace all placeholders with properties
	str := replaceAllPlaceholders(unmarshalledPom, string(bytes))

	var resultPom pom
	if err := xml.Unmarshal([]byte(str), &resultPom); err != nil {
		return nil, fmt.Errorf("parsing xml: %w", err)
	}

	resultPom.path = filepath.Dir(filePath)

	return &resultPom, nil
}

func replaceAllPlaceholders(pom pom, input string) string {
	propsMap := parseProperties(pom.Properties)

	re := regexp.MustCompile(`\$\{([A-Za-z0-9-_.]+)}`)
	return re.ReplaceAllStringFunc(input, func(match string) string {
		// Extract the key inside ${}
		key := re.FindStringSubmatch(match)[1]
		if value, exists := propsMap[key]; exists {
			return value
		}
		return match
	})
}

func parseProperties(properties Properties) map[string]string {
	result := make(map[string]string)
	for _, entry := range properties.Entries {
		result[entry.XMLName.Local] = entry.Value
	}
	return result
}

func toEffectivePom(pomPath string) (pom, error) {
	if !commandExistsInPath("java") {
		return pom{}, fmt.Errorf("can not get effective pom because java command not exist")
	}
	mvn, err := getMvnCommand(pomPath)
	if err != nil {
		return pom{}, err
	}
	cmd := exec.Command(mvn, "help:effective-pom", "-f", pomPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return pom{}, err
	}
	effectivePom, err := getEffectivePomFromConsoleOutput(string(output))
	if err != nil {
		return pom{}, err
	}
	var project pom
	if err := xml.Unmarshal([]byte(effectivePom), &project); err != nil {
		return pom{}, fmt.Errorf("parsing xml: %w", err)
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
