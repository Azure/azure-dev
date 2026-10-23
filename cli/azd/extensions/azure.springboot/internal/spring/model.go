// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

// Dependency identifies a resolved Maven dependency.
type Dependency struct {
	Group    string
	Artifact string
	Scope    string
}

// ModelData contains the effective Maven model and resolved runtime dependencies.
type ModelData struct {
	EffectivePom        []byte
	RuntimeDependencies []Dependency
}

// ModelResolver resolves a trusted application's effective Maven model and runtime dependencies.
type ModelResolver interface {
	Resolve(context.Context, string) (*ModelData, error)
}

type mavenResolver struct {
	writer io.Writer
}

// NewModelResolver uses azd's Maven wrapper and effective-model implementation.
func NewModelResolver(writer io.Writer) ModelResolver {
	return &mavenResolver{writer: writer}
}

func (r *mavenResolver) Resolve(ctx context.Context, root string) (*ModelData, error) {
	runner := azdexec.NewCommandRunner(&azdexec.RunnerOptions{Stdout: r.writer, Stderr: r.writer})
	cli := maven.NewCli(runner)
	cli.SetPath(root, root)
	effectivePom, err := cli.EffectivePom(ctx, filepath.Join(root, "pom.xml"))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &modelError{cause: err}
	}
	dependencies, err := resolveRuntimeDependencies(ctx, root, runner)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &modelError{cause: err}
	}
	return &ModelData{EffectivePom: []byte(effectivePom), RuntimeDependencies: dependencies}, nil
}

// Maven errors can contain repository URLs, credentials, or effective-model output.
type modelError struct {
	cause error
}

func (e *modelError) Error() string {
	return "Maven model resolution failed; run Maven help:effective-pom and dependency:list " +
		"in the trusted project to diagnose"
}

func (e *modelError) Unwrap() error { return e.cause }

type coordinate struct {
	Group    string `xml:"groupId"`
	Artifact string `xml:"artifactId"`
	Scope    string `xml:"scope"`
}

type buildPlugin struct {
	Group      string `xml:"groupId"`
	Artifact   string `xml:"artifactId"`
	Executions []struct {
		Phase         string              `xml:"phase"`
		Goals         []string            `xml:"goals>goal"`
		Configuration pluginConfiguration `xml:"configuration"`
	} `xml:"executions>execution"`
	Configuration pluginConfiguration `xml:"configuration"`
}

type pluginConfiguration struct {
	Skip                 string   `xml:"skip"`
	Classifier           string   `xml:"classifier"`
	Layout               string   `xml:"layout"`
	OutputDirectory      string   `xml:"outputDirectory"`
	Release              string   `xml:"release"`
	Source               string   `xml:"source"`
	Target               string   `xml:"target"`
	Delimiters           []string `xml:"delimiters>delimiter"`
	UseDefaultDelimiters string   `xml:"useDefaultDelimiters"`
	CombineSelf          string   `xml:"combine.self,attr"`
}

func (c pluginConfiguration) withDefaults(parent pluginConfiguration) pluginConfiguration {
	if c.CombineSelf == "override" {
		return c
	}
	if c.Skip == "" {
		c.Skip = parent.Skip
	}
	if c.Classifier == "" {
		c.Classifier = parent.Classifier
	}
	if c.Layout == "" {
		c.Layout = parent.Layout
	}
	if c.OutputDirectory == "" {
		c.OutputDirectory = parent.OutputDirectory
	}
	if c.Release == "" {
		c.Release = parent.Release
	}
	if c.Source == "" {
		c.Source = parent.Source
	}
	if c.Target == "" {
		c.Target = parent.Target
	}
	if len(c.Delimiters) == 0 {
		c.Delimiters = slices.Clone(parent.Delimiters)
	}
	if c.UseDefaultDelimiters == "" {
		c.UseDefaultDelimiters = parent.UseDefaultDelimiters
	}
	return c
}

type buildResource struct {
	Directory  string `xml:"directory"`
	TargetPath string `xml:"targetPath"`
	Filtering  string `xml:"filtering"`
}

type model struct {
	RepackageDirectories []string     `xml:"-"`
	Artifact             string       `xml:"artifactId"`
	Version              string       `xml:"version"`
	Packaging            string       `xml:"packaging"`
	Modules              []string     `xml:"modules>module"`
	Dependencies         []coordinate `xml:"dependencies>dependency"`
	Properties           struct {
		JavaVersion   string `xml:"java.version"`
		Release       string `xml:"maven.compiler.release"`
		Source        string `xml:"maven.compiler.source"`
		Target        string `xml:"maven.compiler.target"`
		RepackageSkip string `xml:"spring-boot.repackage.skip"`
	} `xml:"properties"`
	Build struct {
		Directory string          `xml:"directory"`
		FinalName string          `xml:"finalName"`
		Plugins   []buildPlugin   `xml:"plugins>plugin"`
		Resources []buildResource `xml:"resources>resource"`
	} `xml:"build"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func parseModel(data []byte) (*model, int, error) {
	var m model
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, 0, invalidConfiguration("Maven returned an invalid effective model")
	}
	if len(m.Modules) != 0 || (m.Packaging != "" && m.Packaging != "jar") {
		return nil, 0, unsupportedConfiguration(
			"initialization currently requires a single-module, executable-JAR application")
	}
	if !filepath.IsAbs(m.Build.Directory) || strings.Contains(m.Build.Directory, "${") {
		return nil, 0, invalidConfiguration("Maven effective model must include a resolved absolute build.directory")
	}
	boot := false
	for _, plugin := range m.Build.Plugins {
		if plugin.Group != "org.springframework.boot" || plugin.Artifact != "spring-boot-maven-plugin" {
			continue
		}
		for _, execution := range plugin.Executions {
			if (execution.Phase == "" || execution.Phase == "package") && slices.Contains(execution.Goals, "repackage") {
				config := execution.Configuration.withDefaults(plugin.Configuration)
				if config.Skip == "" {
					config.Skip = m.Properties.RepackageSkip
				}
				if config.Skip != "" && !strings.EqualFold(config.Skip, "false") ||
					config.Classifier != "" || config.Layout == "NONE" {
					return nil, 0, unsupportedConfiguration(
						"custom or disabled Spring Boot JAR repackaging is not yet supported")
				}
				output := config.OutputDirectory
				if output == "" {
					output = m.Build.Directory
				}
				m.RepackageDirectories = append(m.RepackageDirectories, output)
				boot = true
			}
		}
	}
	if !boot {
		return nil, 0, unsupportedConfiguration(
			"an active Spring Boot repackage execution in the Maven package phase is required")
	}
	if !safeName.MatchString(m.Artifact) {
		return nil, 0, invalidConfiguration("Maven artifactId must be a safe file name")
	}
	if m.Build.FinalName == "" {
		m.Build.FinalName = m.Artifact + "-" + m.Version
	}
	if !safeName.MatchString(m.Build.FinalName) {
		return nil, 0, invalidConfiguration("Maven finalName must be a safe file name")
	}
	java, err := effectiveJavaVersion(&m)
	if err != nil {
		return nil, 0, err
	}
	return &m, java, nil
}

func effectiveJavaVersion(m *model) (int, error) {
	configurationVersion := func(config pluginConfiguration) (int, bool, error) {
		return compilerConfigurationVersion(config,
			m.Properties.Release, m.Properties.Source, m.Properties.Target, m.Properties.JavaVersion)
	}
	var versions []int
	for _, plugin := range m.Build.Plugins {
		if plugin.Artifact != "maven-compiler-plugin" ||
			plugin.Group != "" && plugin.Group != "org.apache.maven.plugins" {
			continue
		}
		active := false
		for _, execution := range plugin.Executions {
			if !compilerExecutionRunsDuringPackage(execution.Phase, execution.Goals) {
				continue
			}
			active = true
			version, configured, err := configurationVersion(
				execution.Configuration.withDefaults(plugin.Configuration))
			if err != nil {
				return 0, err
			}
			if !configured {
				return 0, unsupportedConfiguration(
					"a Java release of 17 or newer must be specified in the effective Maven model")
			}
			versions = append(versions, version)
		}
		if active {
			continue
		}
		version, configured, err := configurationVersion(plugin.Configuration)
		if err != nil {
			return 0, err
		}
		if !configured {
			return 0, unsupportedConfiguration(
				"a Java release of 17 or newer must be specified in the effective Maven model")
		}
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		version, configured, err := configurationVersion(pluginConfiguration{})
		if err != nil {
			return 0, err
		}
		if !configured {
			return 0, unsupportedConfiguration(
				"a Java release of 17 or newer must be specified in the effective Maven model")
		}
		versions = append(versions, version)
	}
	java := slices.Max(versions)
	if java < 17 {
		return 0, unsupportedConfiguration(
			"a Java release of 17 or newer must be specified in the effective Maven model")
	}
	return java, nil
}

func compilerConfigurationVersion(
	config pluginConfiguration, release, source, target, javaVersion string,
) (int, bool, error) {
	if config.Release == "" {
		config.Release = release
	}
	if config.Source == "" {
		config.Source = source
	}
	if config.Target == "" {
		config.Target = target
	}
	if config.Release != "" {
		version, err := parseJavaVersion(config.Release, "maven-compiler-plugin release")
		return version, true, err
	}
	if config.Source == "" && config.Target == "" {
		if javaVersion == "" {
			return 0, false, nil
		}
		version, err := parseJavaVersion(javaVersion, "java.version")
		return version, true, err
	}
	version, err := compilerSourceTargetVersion(config.Source, config.Target)
	return version, true, err
}

func compilerSourceTargetVersion(source, target string) (int, error) {
	var sourceVersion, targetVersion int
	var err error
	if source != "" {
		sourceVersion, err = parseJavaVersion(source, "Java compiler source")
		if err != nil {
			return 0, err
		}
	}
	if target != "" {
		targetVersion, err = parseJavaVersion(target, "Java compiler target")
		if err != nil {
			return 0, err
		}
	}
	if sourceVersion > 0 && targetVersion > 0 && sourceVersion > targetVersion {
		return 0, unsupportedConfiguration("Java compiler source must not exceed its target release")
	}
	return max(sourceVersion, targetVersion), nil
}

func parseJavaVersion(value, setting string) (int, error) {
	version, err := strconv.Atoi(value)
	if err != nil {
		return 0, unsupportedConfiguration("%s must specify a resolved Java release", setting)
	}
	return version, nil
}

func compilerExecutionRunsDuringPackage(phase string, goals []string) bool {
	if !slices.Contains(goals, "compile") && !slices.Contains(goals, "testCompile") {
		return false
	}
	if phase == "" {
		return true
	}
	return slices.Contains([]string{
		"validate", "initialize", "generate-sources", "process-sources", "generate-resources",
		"process-resources", "compile", "process-classes", "generate-test-sources", "process-test-sources",
		"generate-test-resources", "process-test-resources", "test-compile", "process-test-classes",
		"test", "prepare-package", "package",
	}, phase)
}

func (m *model) hasDependency(group, artifact string) bool {
	for _, d := range m.Dependencies {
		if d.Group == group && d.Artifact == artifact &&
			(d.Scope == "" || d.Scope == "compile" || d.Scope == "runtime" || d.Scope == "provided") {
			return true
		}
	}
	return false
}

func (m *model) hasPackagedIdentityExtensions() bool {
	for _, d := range m.Dependencies {
		if d.Group == "com.azure" && d.Artifact == "azure-identity-extensions" &&
			(d.Scope == "" || d.Scope == "compile" || d.Scope == "runtime") {
			return true
		}
	}
	return false
}

func projectName(artifact string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(artifact) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func resolveRuntimeDependencies(
	ctx context.Context, root string, runner azdexec.CommandRunner,
) ([]Dependency, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	output, err := os.CreateTemp("", "azd-spring-maven-dependencies-*.txt")
	if err != nil {
		return nil, err
	}
	outputPath := output.Name()
	defer os.Remove(outputPath)
	if err := output.Close(); err != nil {
		return nil, err
	}

	command, err := mavenExecutable(root)
	if err != nil {
		return nil, err
	}
	// cspell:ignore DincludeScope DexcludeTransitive DappendOutput DoutputAbsoluteArtifactFilename DoutputFile
	args := azdexec.NewRunArgs(command,
		"dependency:list",
		"-DincludeScope=runtime",
		"-DexcludeTransitive=false",
		"-DappendOutput=false",
		"-DoutputAbsoluteArtifactFilename=false",
		"-DoutputFile="+outputPath,
		"-f", filepath.Join(root, "pom.xml"),
	).WithCwd(root)
	if _, err := runner.Run(ctx, args); err != nil {
		return nil, err
	}
	tempRoot, err := os.OpenRoot(filepath.Dir(outputPath))
	if err != nil {
		return nil, err
	}
	defer tempRoot.Close()
	data, err := tempRoot.ReadFile(filepath.Base(outputPath))
	if err != nil {
		return nil, err
	}
	return parseRuntimeDependencies(string(data)), nil
}

func mavenExecutable(root string) (string, error) {
	names := []string{"mvnw"}
	if runtime.GOOS == "windows" {
		names = []string{"mvnw.cmd", "mvnw.bat", "mvnw"}
	}
	for _, name := range names {
		candidate := filepath.Join(root, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	command, err := osexec.LookPath("mvn")
	if err != nil {
		return "", errors.New("Maven or a root Maven wrapper is required")
	}
	return command, nil
}

func parseRuntimeDependencies(output string) []Dependency {
	var result []Dependency
	seen := map[string]bool{}
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		parts := strings.Split(fields[0], ":")
		if len(parts) < 5 {
			continue
		}
		scope := parts[len(parts)-1]
		if scope != "compile" && scope != "runtime" {
			continue
		}
		dependency := Dependency{Group: parts[0], Artifact: parts[1], Scope: scope}
		key := dependency.Group + ":" + dependency.Artifact + ":" + dependency.Scope
		if !safeName.MatchString(dependency.Group) || !safeName.MatchString(dependency.Artifact) || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, dependency)
	}
	return result
}
