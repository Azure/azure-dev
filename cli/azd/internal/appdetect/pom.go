package appdetect

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pom represents the top-level structure of a Maven POM file.
type pom struct {
	XmlName                 xml.Name             `xml:"project"`
	Parent                  parent               `xml:"parent"`
	GroupId                 string               `xml:"groupId"`
	ArtifactId              string               `xml:"artifactId"`
	Version                 string               `xml:"version"`
	Modules                 []string             `xml:"modules>module"` // Capture the modules
	Properties              Properties           `xml:"properties"`
	Dependencies            []dependency         `xml:"dependencies>dependency"`
	DependencyManagement    dependencyManagement `xml:"dependencyManagement"`
	Build                   build                `xml:"build"`
	pomFilePath             string
	propertyMap             map[string]string
	dependencyManagementMap map[string]string
}

// Parent represents the parent POM if this project is a module.
type parent struct {
	GroupId      string `xml:"groupId"`
	ArtifactId   string `xml:"artifactId"`
	Version      string `xml:"version"`
	RelativePath string `xml:"relativePath"`
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

const (
	DependencyScopeCompile string = "compile"
	DependencyScopeTest    string = "test"
)

func createEffectivePomOrSimulatedEffectivePom(pomPath string) (pom, error) {
	pom, err := createEffectivePom(pomPath)
	if err == nil {
		return pom, nil
	}
	return createSimulatedEffectivePom(pomPath)
}

// Simulated effective pom not strictly equal to effective pom,
// it just tries best to make sure these item are same to the real effective pom:
//  1. pom.Dependencies. Only care about the groupId/artifactId/version.
//  2. pom.Build.Plugins.
//     2.1. Only care about the groupId/artifactId/version.
//     2.2. Not include the default maven plugins (name with this patten: "maven-xxx-plugin").
func createSimulatedEffectivePom(pomFilePath string) (pom, error) {
	pom, err := unmarshalPomFromFilePath(pomFilePath)
	if err != nil {
		return pom, err
	}
	convertToSimulatedEffectivePom(&pom)
	return pom, nil
}

func convertToSimulatedEffectivePom(pom *pom) {
	setDefaultScopeForDependenciesAndDependencyManagement(pom)
	updateVersionAccordingToPropertiesAndDependencyManagement(pom)
	absorbInformationFromParentAndImportedDependenciesInDependencyManagement(pom)
}

func updateVersionAccordingToPropertiesAndDependencyManagement(pom *pom) {
	createPropertyMapAccordingToProjectProperty(pom)
	addCommonPropertiesLikeProjectGroupIdAndProjectVersionToPropertyMap(pom)
	// replacePropertyPlaceHolderInPropertyMap should run before other replacePropertyPlaceHolderInXxx
	replacePropertyPlaceHolderInPropertyMap(pom)
	// replacePropertyPlaceHolderInGroupId should run before createDependencyManagementMap
	replacePropertyPlaceHolderInGroupId(pom)
	// createDependencyManagementMap run before replacePropertyPlaceHolderInVersion
	createDependencyManagementMap(pom)
	replacePropertyPlaceHolderInVersion(pom)
	updateDependencyVersionAccordingToDependencyManagement(pom)
}

func absorbInformationFromParentAndImportedDependenciesInDependencyManagement(pom *pom) {
	absorbInformationFromParent(pom)
	absorbImportedBomInDependencyManagement(pom)
}

func absorbInformationFromParent(pom *pom) {
	if !parentExists(*pom) {
		slog.DebugContext(context.TODO(), "Skip analyze parent pom because parent not set.",
			"pomFilePath", pom.pomFilePath)
		return
	}
	if absorbInformationFromParentInLocalFileSystem(pom) {
		return
	}
	absorbInformationFromParentInRemoteMavenRepository(pom)
}

func absorbInformationFromParentInLocalFileSystem(pom *pom) bool {
	parentPomFilePath := getParentPomFilePath(*pom)
	if !fileExists(parentPomFilePath) {
		slog.DebugContext(context.TODO(), "Skip analyze parent pom because parent pom file not set.",
			"pomFilePath", pom.pomFilePath)
		return false
	}
	parentEffectivePom, err := createSimulatedEffectivePom(parentPomFilePath)
	if err != nil {
		slog.DebugContext(context.TODO(), "Skip analyze parent pom because analyze parent pom failed.",
			"pomFilePath", pom.pomFilePath)
		return false
	}
	if pom.Parent.GroupId != parentEffectivePom.GroupId ||
		pom.Parent.ArtifactId != parentEffectivePom.ArtifactId ||
		pom.Parent.Version != parentEffectivePom.Version {
		slog.DebugContext(context.TODO(), "Skip analyze parent pom because groupId/artifactId/version not the same.",
			"pomFilePath", pom.pomFilePath)
		return false
	}
	absorbInformationFromParentPom(pom, parentEffectivePom)
	return true
}

func parentExists(pom pom) bool {
	return pom.Parent.GroupId != "" && pom.Parent.ArtifactId != ""
}

func getParentPomFilePath(pom pom) string {
	relativePath := pom.Parent.RelativePath
	if relativePath == "" {
		relativePath = "../pom.xml"
	}
	parentPomFilePath := filepath.Join(filepath.Dir(makePathFitCurrentOs(pom.pomFilePath)),
		makePathFitCurrentOs(relativePath))
	parentPomFilePath = filepath.Clean(parentPomFilePath)
	return parentPomFilePath
}

func makePathFitCurrentOs(filePath string) string {
	if os.PathSeparator == '\\' {
		return strings.ReplaceAll(filePath, "/", "\\")
	} else {
		return strings.ReplaceAll(filePath, "\\", "/")
	}
}

func absorbInformationFromParentInRemoteMavenRepository(pom *pom) {
	p := pom.Parent
	parent, err := getSimulatedEffectivePomFromRemoteMavenRepository(
		p.GroupId, p.ArtifactId, p.Version)
	if err != nil {
		slog.InfoContext(context.TODO(), "Skip absorb parent from remote maven repository.",
			"pomFilePath", pom.pomFilePath, "err", err)
	}
	absorbInformationFromParentPom(pom, parent)
}

func absorbInformationFromParentPom(pom *pom, parent pom) {
	absorbDependencyManagement(pom, parent)
	absorbPropertyMap(pom, parent)
	absorbDependency(pom, parent)
	absorbBuildPlugin(pom, parent)
}

func absorbDependency(pom *pom, toBeAbsorbedPom pom) {
	for _, dep := range toBeAbsorbedPom.Dependencies {
		if !containsDependency(pom.Dependencies, dep) {
			pom.Dependencies = append(pom.Dependencies, dep)
		}
	}
}

func containsDependency(deps []dependency, targetDep dependency) bool {
	for _, dep := range deps {
		if dep.GroupId == targetDep.GroupId && dep.ArtifactId == targetDep.ArtifactId {
			return true
		}
	}
	return false
}

func absorbBuildPlugin(pom *pom, toBeAbsorbedPom pom) {
	for _, p := range toBeAbsorbedPom.Build.Plugins {
		if !containsBuildPlugin(pom.Build.Plugins, p) {
			pom.Build.Plugins = append(pom.Build.Plugins, p)
		}
	}
}

func containsBuildPlugin(plugins []plugin, targetPlugin plugin) bool {
	for _, p := range plugins {
		if p.GroupId == targetPlugin.GroupId && p.ArtifactId == targetPlugin.ArtifactId {
			return true
		}
	}
	return false
}

func absorbImportedBomInDependencyManagement(pom *pom) {
	for _, dep := range pom.DependencyManagement.Dependencies {
		if dep.Scope != "import" {
			continue
		}
		toBeAbsorbedPom, err := getSimulatedEffectivePomFromRemoteMavenRepository(
			dep.GroupId, dep.ArtifactId, dep.Version)
		if err != nil {
			slog.InfoContext(context.TODO(), "Skip absorb imported bom from remote maven repository.",
				"pomFilePath", pom.pomFilePath, "err", err)
		}
		absorbDependencyManagement(pom, toBeAbsorbedPom)
	}
}

func absorbPropertyMap(pom *pom, toBeAbsorbedPom pom) {
	for key, value := range toBeAbsorbedPom.propertyMap {
		addToPropertyMapIfKeyIsNew(pom, key, value)
	}
	replacePropertyPlaceHolderInPropertyMap(pom)
	replacePropertyPlaceHolderInGroupId(pom)
	replacePropertyPlaceHolderInVersion(pom)
	updateDependencyVersionAccordingToDependencyManagement(pom)
}

func absorbDependencyManagement(pom *pom, toBeAbsorbedPom pom) {
	for key, value := range toBeAbsorbedPom.dependencyManagementMap {
		addNewDependencyInDependencyManagementIfDependencyIsNew(pom, key, value)
	}
	updateDependencyVersionAccordingToDependencyManagement(pom)
}

func getSimulatedEffectivePomFromRemoteMavenRepository(groupId string, artifactId string, version string) (pom, error) {
	requestUrl := getRemoteMavenRepositoryUrl(groupId, artifactId, version)
	bytes, err := download(requestUrl)
	if err != nil {
		return pom{}, err
	}
	var result pom
	if err := xml.Unmarshal(bytes, &result); err != nil {
		return pom{}, fmt.Errorf("parsing xml: %w", err)
	}
	convertToSimulatedEffectivePom(&result)
	for _, value := range result.dependencyManagementMap {
		if isVariable(value) {
			log.Printf("Unresolved property: value = %s\n", value)
		}
	}
	return result, nil
}

func getRemoteMavenRepositoryUrl(groupId string, artifactId string, version string) string {
	return fmt.Sprintf("https://repo.maven.apache.org/maven2/%s/%s/%s/%s-%s.pom",
		strings.ReplaceAll(groupId, ".", "/"), artifactId, version, artifactId, version)
}

func unmarshalPomFromFilePath(pomFilePath string) (pom, error) {
	bytes, err := os.ReadFile(pomFilePath)
	if err != nil {
		return pom{}, err
	}
	result, err := unmarshalPomFromBytes(bytes)
	if err != nil {
		return pom{}, err
	}
	result.pomFilePath = pomFilePath
	return result, nil
}

func setDefaultScopeForDependenciesAndDependencyManagement(pom *pom) {
	for i, dep := range pom.Dependencies {
		if dep.Scope == "" {
			pom.Dependencies[i].Scope = DependencyScopeCompile
		}
	}
	for i, dep := range pom.DependencyManagement.Dependencies {
		if dep.Scope == "" {
			pom.DependencyManagement.Dependencies[i].Scope = DependencyScopeCompile
		}
	}
}

func unmarshalPomFromString(pomString string) (pom, error) {
	return unmarshalPomFromBytes([]byte(pomString))
}

func unmarshalPomFromBytes(pomBytes []byte) (pom, error) {
	var unmarshalledPom pom
	if err := xml.Unmarshal(pomBytes, &unmarshalledPom); err != nil {
		return pom{}, fmt.Errorf("parsing xml: %w", err)
	}
	return unmarshalledPom, nil
}

func addCommonPropertiesLikeProjectGroupIdAndProjectVersionToPropertyMap(pom *pom) {
	addToPropertyMapIfKeyIsNew(pom, "project.groupId", pom.GroupId)
	pomVersion := pom.Version
	if pomVersion == "" {
		pomVersion = pom.Parent.Version
	}
	addToPropertyMapIfKeyIsNew(pom, "project.version", pomVersion)
}

func createPropertyMapAccordingToProjectProperty(pom *pom) {
	pom.propertyMap = make(map[string]string) // propertyMap only create once
	for _, entry := range pom.Properties.Entries {
		addToPropertyMapIfKeyIsNew(pom, entry.XMLName.Local, entry.Value)
	}
}

func addToPropertyMapIfKeyIsNew(pom *pom, key string, value string) {
	if _, ok := pom.propertyMap[key]; ok {
		return
	}
	pom.propertyMap[key] = value
}

func replacePropertyPlaceHolderInPropertyMap(pom *pom) {
	for key, value := range pom.propertyMap {
		if isVariable(value) {
			variableName := getVariableName(value)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				pom.propertyMap[key] = variableValue
			}
		}
	}
}

func replacePropertyPlaceHolderInGroupId(pom *pom) {
	for i, dep := range pom.DependencyManagement.Dependencies {
		if isVariable(dep.GroupId) {
			variableName := getVariableName(dep.GroupId)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				pom.DependencyManagement.Dependencies[i].GroupId = variableValue
			}
		}
	}
	for i, dep := range pom.Dependencies {
		if isVariable(dep.GroupId) {
			variableName := getVariableName(dep.GroupId)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				pom.Dependencies[i].GroupId = variableValue
			}
		}
	}
	for i, dep := range pom.Build.Plugins {
		if isVariable(dep.GroupId) {
			variableName := getVariableName(dep.GroupId)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				pom.Build.Plugins[i].GroupId = variableValue
			}
		}
	}
}

func replacePropertyPlaceHolderInVersion(pom *pom) {
	for key, value := range pom.dependencyManagementMap {
		if isVariable(value) {
			variableName := getVariableName(value)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				updateDependencyVersionInDependencyManagement(pom, key, variableValue)
			}
		}
	}
	for i, dep := range pom.Dependencies {
		if isVariable(dep.Version) {
			variableName := getVariableName(dep.Version)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				pom.Dependencies[i].Version = variableValue
			}
		}
	}
	for i, dep := range pom.Build.Plugins {
		if isVariable(dep.Version) {
			variableName := getVariableName(dep.Version)
			if variableValue, ok := pom.propertyMap[variableName]; ok {
				pom.Build.Plugins[i].Version = variableValue
			}
		}
	}
}

const variablePrefix = "${"
const variableSuffix = "}"

func isVariable(value string) bool {
	return strings.HasPrefix(value, variablePrefix) && strings.HasSuffix(value, variableSuffix)
}

func getVariableName(value string) string {
	return strings.TrimSuffix(strings.TrimPrefix(value, variablePrefix), variableSuffix)
}

func toDependencyManagementMapKey(dependency dependency) string {
	return fmt.Sprintf("%s:%s:%s", dependency.GroupId, dependency.ArtifactId, dependency.Scope)
}

func createDependencyFromDependencyManagementMapKeyAndVersion(key string, version string) dependency {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return dependency{}
	}
	return dependency{parts[0], parts[1], version, parts[2]}
}

func createDependencyManagementMap(pom *pom) {
	pom.dependencyManagementMap = make(map[string]string) // dependencyManagementMap only create once
	for _, dep := range pom.DependencyManagement.Dependencies {
		pom.dependencyManagementMap[toDependencyManagementMapKey(dep)] = dep.Version
	}
}

func addNewDependencyInDependencyManagementIfDependencyIsNew(pom *pom, key string, value string) {
	if value == "" {
		log.Printf("error: add dependency management without version")
		return
	}
	if _, ok := pom.dependencyManagementMap[key]; ok {
		return
	}
	// always make sure DependencyManagement and dependencyManagementMap synced
	pom.dependencyManagementMap[key] = value
	pom.DependencyManagement.Dependencies = append(pom.DependencyManagement.Dependencies,
		createDependencyFromDependencyManagementMapKeyAndVersion(key, value))
}

// always make sure DependencyManagement and dependencyManagementMap synced
func updateDependencyVersionInDependencyManagement(pom *pom, key string, value string) {
	pom.dependencyManagementMap[key] = value
	for i, dep := range pom.DependencyManagement.Dependencies {
		currentKey := toDependencyManagementMapKey(dep)
		if currentKey == key {
			pom.DependencyManagement.Dependencies[i].Version = value
		}
	}
}

func updateDependencyVersionAccordingToDependencyManagement(pom *pom) {
	for i, dep := range pom.Dependencies {
		if strings.TrimSpace(dep.Version) != "" {
			continue
		}
		key := toDependencyManagementMapKey(dep)
		if managedVersion, ok := pom.dependencyManagementMap[key]; ok {
			pom.Dependencies[i].Version = managedVersion
		} else if dep.Scope == DependencyScopeTest {
			dep.Scope = DependencyScopeCompile
			key = toDependencyManagementMapKey(dep)
			if managedVersion, ok = pom.dependencyManagementMap[key]; ok {
				pom.Dependencies[i].Version = managedVersion
			}
		}
	}
}

func createEffectivePom(pomPath string) (pom, error) {
	if !commandExistsInPath("java") {
		return pom{}, fmt.Errorf("can not get effective pom because java command not exist")
	}
	mvn, err := getMvnCommandFromPath(pomPath)
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
	var resultPom pom
	if err := xml.Unmarshal([]byte(effectivePom), &resultPom); err != nil {
		return pom{}, fmt.Errorf("parsing xml: %w", err)
	}
	resultPom.pomFilePath = pomPath
	return resultPom, nil
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
