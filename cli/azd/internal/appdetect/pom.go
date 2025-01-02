package appdetect

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

// pom represents the top-level structure of a Maven POM file.
type pom struct {
	XmlName                 xml.Name             `xml:"project"`
	Parent                  parent               `xml:"parent"`
	GroupId                 string               `xml:"groupId"`
	ArtifactId              string               `xml:"artifactId"`
	Version                 string               `xml:"version"`
	Properties              Properties           `xml:"properties"`
	Modules                 []string             `xml:"modules>module"`
	Dependencies            []dependency         `xml:"dependencies>dependency"`
	DependencyManagement    dependencyManagement `xml:"dependencyManagement"`
	Profiles                []profile            `xml:"profiles>profile"`
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

type profile struct {
	Id                      string               `xml:"id"`
	ActiveByDefault         string               `xml:"activation>activeByDefault"`
	Properties              Properties           `xml:"properties"`
	Modules                 []string             `xml:"modules>module"` // Capture the modules
	Dependencies            []dependency         `xml:"dependencies>dependency"`
	DependencyManagement    dependencyManagement `xml:"dependencyManagement"`
	Build                   build                `xml:"build"`
	propertyMap             map[string]string
	dependencyManagementMap map[string]string
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

func createEffectivePomOrSimulatedEffectivePom(ctx context.Context, mvnCli *maven.Cli, pomPath string) (pom, error) {
	effectivePom, err := createEffectivePom(ctx, mvnCli, pomPath)
	if err == nil {
		effectivePom.pomFilePath = pomPath
		return effectivePom, nil
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
	setDefaultScopeForDependenciesInAllPlaces(pom)

	createPropertyMapAccordingToProjectProperty(pom)
	addCommonPropertiesLikeProjectGroupIdAndProjectVersionToPropertyMap(pom)
	// replacePropertyPlaceHolderInPropertyMap should run before other replacePropertyPlaceHolderInXxx
	replacePropertyPlaceHolderInPropertyMap(pom)
	// replacePropertyPlaceHolderInGroupId should run before createDependencyManagementMap
	replacePropertyPlaceHolderInGroupId(pom)
	// createDependencyManagementMap run before replacePropertyPlaceHolderInVersion
	createDependencyManagementMap(pom)

	// active profile has higher priority than parent and imported bom in dependency management
	absorbInformationFromActiveProfile(pom)
	// replacePropertyPlaceHolderInVersion should run after absorbInformationFromActiveProfile
	replacePropertyPlaceHolderInVersion(pom)
	absorbInformationFromParentAndImportedDependenciesInDependencyManagement(pom)
	// updateDependencyVersionAccordingToDependencyManagement should run after absorbInformationFromActiveProfile
	updateDependencyVersionAccordingToDependencyManagement(pom)
}

func absorbInformationFromActiveProfile(pom *pom) {
	for i := range pom.Profiles {
		if pom.Profiles[i].ActiveByDefault != "true" {
			continue
		}
		absorbPropertyMap(pom, pom.Profiles[i].propertyMap, true)
		absorbDependencyManagement(pom, pom.Profiles[i].dependencyManagementMap, true)
		absorbDependencies(pom, pom.Profiles[i].Dependencies)
		absorbBuildPlugins(pom, pom.Profiles[i].Build.Plugins)
	}
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
			"ArtifactId", pom.ArtifactId, "err", err)
	}
	absorbInformationFromParentPom(pom, parent)
}

func absorbInformationFromParentPom(pom *pom, parent pom) {
	absorbPropertyMap(pom, parent.propertyMap, false)
	absorbDependencyManagement(pom, parent.dependencyManagementMap, false)
	absorbDependencies(pom, parent.Dependencies)
	absorbBuildPlugins(pom, parent.Build.Plugins)
}

func absorbDependencies(pom *pom, dependencies []dependency) {
	for _, dep := range dependencies {
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

func absorbBuildPlugins(pom *pom, plugins []plugin) {
	for _, p := range plugins {
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
				"ArtifactId", pom.ArtifactId, "err", err)
		}
		absorbDependencyManagement(pom, toBeAbsorbedPom.dependencyManagementMap, false)
	}
}

func absorbPropertyMap(pom *pom, propertyMap map[string]string, override bool) {
	for key, value := range propertyMap {
		updatePropertyMap(pom.propertyMap, key, value, override)
	}
	replacePropertyPlaceHolderInPropertyMap(pom)
	replacePropertyPlaceHolderInGroupId(pom)
	replacePropertyPlaceHolderInVersion(pom)
}

func absorbDependencyManagement(pom *pom, dependencyManagementMap map[string]string, override bool) {
	for key, value := range dependencyManagementMap {
		updateDependencyManagement(pom, key, value, override)
	}
}

func getSimulatedEffectivePomFromRemoteMavenRepository(groupId string, artifactId string, version string) (pom, error) {
	requestUrl := getRemoteMavenRepositoryUrl(groupId, artifactId, version)
	bytes, err := internal.Download(requestUrl)
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

func setDefaultScopeForDependenciesInAllPlaces(pom *pom) {
	setDefaultScopeForDependencies(pom.Dependencies)
	setDefaultScopeForDependencies(pom.DependencyManagement.Dependencies)
	for i := range pom.Profiles {
		setDefaultScopeForDependencies(pom.Profiles[i].Dependencies)
		setDefaultScopeForDependencies(pom.Profiles[i].DependencyManagement.Dependencies)
	}
}

func setDefaultScopeForDependencies(dependencies []dependency) {
	for i := range dependencies {
		if dependencies[i].Scope == "" {
			dependencies[i].Scope = DependencyScopeCompile
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
	updatePropertyMap(pom.propertyMap, "project.groupId", pom.GroupId, false)
	pomVersion := pom.Version
	if pomVersion == "" {
		pomVersion = pom.Parent.Version
	}
	updatePropertyMap(pom.propertyMap, "project.version", pomVersion, false)
}

func createPropertyMapAccordingToProjectProperty(pom *pom) {
	pom.propertyMap = make(map[string]string) // propertyMap only create once
	for _, entry := range pom.Properties.Entries {
		updatePropertyMap(pom.propertyMap, entry.XMLName.Local, entry.Value, false)
	}
	for i := range pom.Profiles {
		pom.Profiles[i].propertyMap = make(map[string]string)
		for _, entry := range pom.Profiles[i].Properties.Entries {
			updatePropertyMap(pom.Profiles[i].propertyMap, entry.XMLName.Local, entry.Value, false)
		}
	}
}

func updatePropertyMap(propertyMap map[string]string, key string, value string, override bool) {
	if _, ok := propertyMap[key]; !override && ok {
		return
	}
	propertyMap[key] = value
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
	replacePropertyPlaceHolderInDependenciesGroupId(pom.DependencyManagement.Dependencies, pom.propertyMap)
	replacePropertyPlaceHolderInDependenciesGroupId(pom.Dependencies, pom.propertyMap)
	replacePropertyPlaceHolderInPluginsGroupId(pom.Build.Plugins, pom.propertyMap)
	for i := range pom.Profiles {
		replacePropertyPlaceHolderInDependenciesGroupId(pom.Profiles[i].DependencyManagement.Dependencies,
			pom.propertyMap)
		replacePropertyPlaceHolderInDependenciesGroupId(pom.Profiles[i].Dependencies, pom.propertyMap)
		replacePropertyPlaceHolderInPluginsGroupId(pom.Profiles[i].Build.Plugins, pom.propertyMap)
	}
}

func replacePropertyPlaceHolderInDependenciesGroupId(dependencies []dependency, propertyMap map[string]string) {
	for i, dep := range dependencies {
		if isVariable(dep.GroupId) {
			variableName := getVariableName(dep.GroupId)
			if variableValue, ok := propertyMap[variableName]; ok {
				dependencies[i].GroupId = variableValue
			}
		}
	}
}

func replacePropertyPlaceHolderInPluginsGroupId(plugins []plugin, propertyMap map[string]string) {
	for i, dep := range plugins {
		if isVariable(dep.GroupId) {
			variableName := getVariableName(dep.GroupId)
			if variableValue, ok := propertyMap[variableName]; ok {
				plugins[i].GroupId = variableValue
			}
		}
	}
}

func replacePropertyPlaceHolderInVersion(pom *pom) {
	replacePropertyPlaceHolderInDependencyManagementVersion(pom.dependencyManagementMap,
		pom.DependencyManagement.Dependencies, pom.propertyMap)
	replacePropertyPlaceHolderInDependenciesVersion(pom.Dependencies, pom.propertyMap)
	replacePropertyPlaceHolderInBuildPluginsVersion(pom.Build.Plugins, pom.propertyMap)
	for i := range pom.Profiles {
		replacePropertyPlaceHolderInDependencyManagementVersion(pom.Profiles[i].dependencyManagementMap,
			pom.Profiles[i].DependencyManagement.Dependencies, pom.propertyMap)
		replacePropertyPlaceHolderInDependenciesVersion(pom.Profiles[i].Dependencies, pom.propertyMap)
		replacePropertyPlaceHolderInBuildPluginsVersion(pom.Profiles[i].Build.Plugins, pom.propertyMap)
	}
}

func replacePropertyPlaceHolderInDependencyManagementVersion(dependencyManagementMap map[string]string,
	dependencies []dependency, propertyMap map[string]string) {
	for key, value := range dependencyManagementMap {
		if isVariable(value) {
			variableName := getVariableName(value)
			if variableValue, ok := propertyMap[variableName]; ok {
				updateDependencyVersionInDependencyManagement(dependencyManagementMap,
					dependencies, key, variableValue)
			}
		}
	}
}

func replacePropertyPlaceHolderInDependenciesVersion(dependencies []dependency, propertyMap map[string]string) {
	for i, dep := range dependencies {
		if isVariable(dep.Version) {
			variableName := getVariableName(dep.Version)
			if variableValue, ok := propertyMap[variableName]; ok {
				dependencies[i].Version = variableValue
			}
		}
	}
}

func replacePropertyPlaceHolderInBuildPluginsVersion(plugins []plugin, propertyMap map[string]string) {
	for i, dep := range plugins {
		if isVariable(dep.Version) {
			variableName := getVariableName(dep.Version)
			if variableValue, ok := propertyMap[variableName]; ok {
				plugins[i].Version = variableValue
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
	for i := range pom.Profiles {
		pom.Profiles[i].dependencyManagementMap = make(map[string]string)
		for _, dep := range pom.Profiles[i].DependencyManagement.Dependencies {
			pom.Profiles[i].dependencyManagementMap[toDependencyManagementMapKey(dep)] = dep.Version
		}
	}
}

func updateDependencyManagement(pom *pom, key string, value string, override bool) {
	if value == "" {
		log.Printf("error: add dependency management without version")
		return
	}
	if _, alreadyExist := pom.dependencyManagementMap[key]; !override && alreadyExist {
		return
	}
	// always make sure DependencyManagement and dependencyManagementMap synced
	pom.dependencyManagementMap[key] = value
	pom.DependencyManagement.Dependencies = append(pom.DependencyManagement.Dependencies,
		createDependencyFromDependencyManagementMapKeyAndVersion(key, value))
}

// always make sure DependencyManagement and dependencyManagementMap synced
func updateDependencyVersionInDependencyManagement(dependencyManagementMap map[string]string,
	dependencies []dependency, key string, value string) {
	dependencyManagementMap[key] = value
	for i, dep := range dependencies {
		currentKey := toDependencyManagementMapKey(dep)
		if currentKey == key {
			dependencies[i].Version = value
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

func createEffectivePom(ctx context.Context, mvnCli *maven.Cli, pomPath string) (pom, error) {
	effectivePom, err := mvnCli.EffectivePom(ctx, pomPath)
	if err != nil {
		return pom{}, err
	}
	var resultPom pom
	err = xml.Unmarshal([]byte(effectivePom), &resultPom)
	return resultPom, err
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	} else {
		return false
	}
}
