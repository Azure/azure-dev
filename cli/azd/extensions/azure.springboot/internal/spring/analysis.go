// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ResourceKind identifies an application's logical resource requirement.
type ResourceKind string

const (
	// MySQL identifies a MySQL database.
	MySQL ResourceKind = "mysql"
	// PostgreSQL identifies a PostgreSQL database.
	PostgreSQL ResourceKind = "postgresql"
	// Foundry identifies a Microsoft Foundry model endpoint.
	Foundry ResourceKind = "foundry"
)

// Evidence identifies the property and source establishing a requirement, without its value.
type Evidence struct {
	Property string
	Source   string
}

// Requirement describes a logical resource independently of an Azure deployment.
type Requirement struct {
	Kind           ResourceKind
	Name           string
	Authentication string
	Evidence       Evidence
	JDBCOptions    string
	Driver         string
	Trace          []Evidence
}

// FoundryRequirement describes an Azure OpenAI-compatible model deployment.
type FoundryRequirement struct {
	ChatDeploymentName      string
	EmbeddingDeploymentName string
	Authentication          string
	Evidence                []Evidence
}

// Application is the resolved, credential-free input to deployment planning.
type Application struct {
	ID                     string
	Name                   string
	JavaVersion            int
	JarName                string
	BuildDirectory         string
	JarDirectory           string
	Port                   int
	Profiles               []string
	Embedded               *EmbeddedBinding
	Requirements           []Requirement
	Foundry                *FoundryRequirement
	MavenWrapper           bool
	ExternalConfigurations []string
	IdentityExtensions     bool
	Dockerfile             string
	InputEvidence          []Evidence
	InputReferences        map[string][]Evidence
}

var databaseName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)
var modelDeploymentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,63}$`)

const (
	defaultFoundryChatModelName         = "gpt-5.6-sol"
	defaultFoundryChatModelVersion      = "2026-07-09"
	defaultFoundryEmbeddingModelName    = "text-embedding-3-small"
	defaultFoundryEmbeddingModelVersion = "1"
)

var customDatasource = regexp.MustCompile(
	`(?s)@(?:org\.springframework\.context\.annotation\.)?Bean\b(?:\s*\([^()]*\))?` +
		`(?:[^{};@]|@[\w.$]+(?:\s*\([^()]*\))?)*?\b(?:DataSource|ConnectionFactory|HikariDataSource|` +
		`DriverManagerDataSource|BasicDataSource|PoolDataSource)\s+[\w$]+\s*\(` +
		`|new\s+(?:[\w$]+\.)*(?:HikariDataSource|DriverManagerDataSource|BasicDataSource|PoolDataSource)\s*\(`)
var runtimeActivation = regexp.MustCompile(`\.(?:setAdditionalProfiles|setActiveProfiles|setDefaultProfiles)\s*\(`)
var builderProfileActivation = regexp.MustCompile(
	`(?s)\bnew\s+(?:[\w$]+\.)*SpringApplicationBuilder\s*\([^;]*?\)[^;]*?\.profiles\s*\(`)
var javaCommentsAndLiterals = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\r\n]*|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'`)

// Analyze infers active requirements, not all resource capabilities on the classpath.
func Analyze(ctx context.Context, directory string, resolver ModelResolver, lookup LookupEnv) (*Application, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("opening application: %w", err)
	}
	defer root.Close()
	if _, err := root.Stat("pom.xml"); err != nil {
		return nil, unsupportedConfiguration(
			"a root Maven pom.xml is required; Gradle and multi-module builds are not yet supported")
	}
	if err := inspectCode(ctx, root); err != nil {
		return nil, err
	}
	data, err := resolver.Resolve(ctx, directory)
	if err != nil {
		return nil, err
	}
	m, java, err := parseModel(data.EffectivePom)
	if err != nil {
		return nil, err
	}
	for _, dependency := range data.RuntimeDependencies {
		m.Dependencies = append(m.Dependencies, coordinate{
			Group: dependency.Group, Artifact: dependency.Artifact, Scope: dependency.Scope,
		})
	}
	projectDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolving application directory: %w", err)
	}
	if err := validateResourceLayout(m, projectDirectory); err != nil {
		return nil, err
	}
	buildDirectory, err := filepath.Rel(projectDirectory, m.Build.Directory)
	if err != nil || !filepath.IsLocal(buildDirectory) || buildDirectory == "." {
		return nil, unsupportedConfiguration(
			"Maven build.directory must be a subdirectory of the application Docker context")
	}
	jarDirectory := buildDirectory
	for i, output := range m.RepackageDirectories {
		if strings.Contains(output, "${") {
			return nil, unsupportedConfiguration("Spring Boot repackage outputDirectory must be resolved")
		}
		if !filepath.IsAbs(output) {
			output = filepath.Join(projectDirectory, output)
		}
		relative, err := filepath.Rel(projectDirectory, output)
		if err != nil || !filepath.IsLocal(relative) || relative == "." {
			return nil, unsupportedConfiguration(
				"Spring Boot repackage outputDirectory must be a subdirectory of the application Docker context")
		}
		if i > 0 && relative != jarDirectory {
			return nil, unsupportedConfiguration("active Spring Boot repackage executions have different output directories")
		}
		jarDirectory = relative
	}
	c, err := readConfiguration(ctx, root, lookup)
	if err != nil {
		return nil, err
	}
	webApplicationType, err := c.resolve("spring.main.web-application-type")
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(webApplicationType), "none") {
		return nil, unsupportedConfiguration(
			"spring.main.web-application-type=none is not supported; current deployment requires an HTTP web application")
	}
	serverSSL, err := c.resolve("server.ssl.enabled")
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(serverSSL), "true") {
		return nil, unsupportedConfiguration(
			"server.ssl.enabled=true is not supported; Container Apps ingress forwards HTTP to the application")
	}
	port, err := c.integer("server.port", 8080)
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, invalidConfiguration("server.port must be between 1 and 65535")
	}
	a := &Application{
		ID:   "app",
		Name: projectName(m.Artifact), JavaVersion: java, JarName: m.Build.FinalName + ".jar",
		BuildDirectory: filepath.ToSlash(buildDirectory),
		JarDirectory:   filepath.ToSlash(jarDirectory),
		Port:           port, Profiles: c.profiles,
		ExternalConfigurations: c.externalFiles,
		IdentityExtensions:     m.hasPackagedIdentityExtensions(),
		InputEvidence:          slices.Clone(c.traces["server.port"]),
		InputReferences:        c.placeholderReferences(),
	}
	if info, err := root.Lstat("Dockerfile"); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, unsupportedConfiguration("the root Dockerfile must be a regular file")
		}
		a.Dockerfile = "Dockerfile"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspecting root Dockerfile: %w", err)
	}
	if a.Name == "" {
		return nil, invalidConfiguration("artifactId must contain at least one letter or digit")
	}
	if _, err := root.Stat("mvnw"); err == nil {
		a.MavenWrapper = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspecting Maven wrapper: %w", err)
	}
	for _, key := range []string{"spring.r2dbc.url", "spring.data.mongodb.uri", "spring.data.redis.host"} {
		value, err := c.resolve(key)
		if err != nil {
			return nil, err
		}
		if value != "" {
			return nil, unsupportedConfiguration("%s identifies a resource whose provisioning is not yet supported", key)
		}
	}
	for _, starter := range []string{"spring-boot-starter-data-mongodb", "spring-boot-starter-data-mongodb-reactive",
		"spring-boot-starter-data-redis", "spring-boot-starter-data-redis-reactive", "spring-boot-starter-data-r2dbc"} {
		if m.hasDependency("org.springframework.boot", starter) {
			return nil, unsupportedConfiguration("%s requires a resource detector not yet supported", starter)
		}
	}
	for key := range c.values {
		if strings.HasSuffix(key, ".datasource.url") && key != "spring.datasource.url" {
			return nil, unsupportedConfiguration("a custom datasource property requires binding analysis not yet supported")
		}
	}
	requirement, embedded, err := detectDatasource(m, c)
	if err != nil {
		return nil, err
	}
	a.InputEvidence = append(a.InputEvidence, c.traces["spring.datasource.url"]...)
	if embedded {
		a.Embedded, err = resolveEmbedded(m, c)
		if err != nil {
			return nil, err
		}
	}
	if requirement != nil {
		a.Requirements = []Requirement{*requirement}
	}
	a.Foundry, err = detectFoundry(m, c)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func detectFoundry(m *model, c *configuration) (*FoundryRequirement, error) {
	const (
		chatModelProperty           = "spring.ai.model.chat"
		embeddingModelProperty      = "spring.ai.model.embedding"
		endpointProperty            = "spring.ai.azure.openai.endpoint"
		apiKeyProperty              = "spring.ai.azure.openai.api-key"
		publicKeyProperty           = "spring.ai.azure.openai.openai-api-key"
		chatDeploymentProperty      = "spring.ai.azure.openai.chat.options.deployment-name"
		embeddingDeploymentProperty = "spring.ai.azure.openai.embedding.options.deployment-name"
		azureOpenAIProvider         = "azure-openai"
	)
	properties := []string{
		endpointProperty,
		apiKeyProperty,
		chatDeploymentProperty,
		embeddingDeploymentProperty,
	}
	sharedConfigured := configuredProperty(c, endpointProperty) || configuredProperty(c, apiKeyProperty)
	chatConfigured := configuredProperty(c, chatDeploymentProperty)
	embeddingConfigured := configuredProperty(c, embeddingDeploymentProperty)
	chatModel, chatModelPresent, err := resolvedProvider(
		c, chatModelProperty, sharedConfigured || chatConfigured)
	if err != nil {
		return nil, err
	}
	embeddingModel, embeddingModelPresent, err := resolvedProvider(
		c, embeddingModelProperty, embeddingConfigured)
	if err != nil {
		return nil, err
	}
	active := sharedConfigured || chatConfigured || embeddingConfigured ||
		chatModelPresent && strings.EqualFold(chatModel, azureOpenAIProvider) ||
		embeddingModelPresent && strings.EqualFold(embeddingModel, azureOpenAIProvider)
	if !active {
		return nil, nil
	}
	if !m.hasDependency("org.springframework.ai", "spring-ai-starter-model-azure-openai") {
		return nil, unsupportedConfiguration(
			"Azure OpenAI configuration requires org.springframework.ai:spring-ai-starter-model-azure-openai")
	}
	if chatConfigured && chatModelPresent && !strings.EqualFold(chatModel, azureOpenAIProvider) {
		return nil, unsupportedConfiguration(
			"%s cannot configure Azure OpenAI while %s selects %s",
			chatDeploymentProperty, chatModelProperty, chatModel)
	}
	if embeddingConfigured && embeddingModelPresent && !strings.EqualFold(embeddingModel, azureOpenAIProvider) {
		return nil, unsupportedConfiguration(
			"%s cannot configure Azure OpenAI while %s selects %s",
			embeddingDeploymentProperty, embeddingModelProperty, embeddingModel)
	}
	chatEnabled := chatConfigured || chatModelPresent && strings.EqualFold(chatModel, azureOpenAIProvider)
	embeddingEnabled := embeddingConfigured ||
		embeddingModelPresent && strings.EqualFold(embeddingModel, azureOpenAIProvider)
	if !chatEnabled && !embeddingEnabled && !chatModelPresent {
		// Preserve chat as the legacy default when only shared Azure OpenAI properties are configured.
		chatEnabled = true
	}
	if !chatEnabled && !embeddingEnabled {
		return nil, nil
	}
	if _, present := c.raw(publicKeyProperty); present {
		value, err := c.resolve(publicKeyProperty)
		if err != nil {
			if _, missing := errors.AsType[*missingInput](err); missing {
				return nil, unsupportedConfiguration(
					"%s cannot be unresolved because a non-empty value selects public OpenAI instead of Azure OpenAI",
					publicKeyProperty)
			}
			return nil, err
		}
		if strings.TrimSpace(value) != "" {
			return nil, unsupportedConfiguration(
				"%s selects public OpenAI instead of the provisioned Azure OpenAI account",
				publicKeyProperty)
		}
	}
	if endpoint, present := c.raw(endpointProperty); present {
		value := strings.TrimSpace(endpoint.text)
		if value != "" {
			_, fallback, placeholderOnly := runtimePlaceholder(value)
			if !placeholderOnly || fallback != "" {
				return nil, unsupportedConfiguration(
					"%s in %s must be empty or a runtime placeholder without a default; "+
						"deployment provisions the Foundry endpoint",
					endpoint.key, endpoint.source)
			}
		}
	}
	chatDeploymentName, err := foundryDeploymentName(c, chatDeploymentProperty, defaultFoundryChatModelName, chatEnabled)
	if err != nil {
		return nil, err
	}
	embeddingDeploymentName, err := foundryDeploymentName(
		c, embeddingDeploymentProperty, defaultFoundryEmbeddingModelName, embeddingEnabled)
	if err != nil {
		return nil, err
	}
	if chatDeploymentName != "" && chatDeploymentName == embeddingDeploymentName {
		return nil, invalidConfiguration(
			"%s and %s must use different deployment names",
			chatDeploymentProperty, embeddingDeploymentProperty)
	}
	authentication := "managedIdentity"
	if apiKey, present := c.raw(apiKeyProperty); present && strings.TrimSpace(apiKey.text) != "" {
		authentication = "apiKey"
	}
	var evidence []Evidence
	for _, key := range append(properties, chatModelProperty, embeddingModelProperty) {
		if value, present := c.raw(key); present {
			item := Evidence{Property: value.key, Source: value.source}
			if !slices.Contains(evidence, item) {
				evidence = append(evidence, item)
			}
		}
	}
	return &FoundryRequirement{
		ChatDeploymentName:      chatDeploymentName,
		EmbeddingDeploymentName: embeddingDeploymentName,
		Authentication:          authentication,
		Evidence:                evidence,
	}, nil
}

func resolvedProvider(c *configuration, property string, required bool) (string, bool, error) {
	if _, present := c.raw(property); !present {
		return "", false, nil
	}
	value, err := c.resolve(property)
	if err != nil {
		if _, missing := errors.AsType[*missingInput](err); missing && !required {
			return "", false, nil
		}
		return "", true, err
	}
	return strings.TrimSpace(value), true, nil
}

func configuredProperty(c *configuration, property string) bool {
	value, present := c.raw(property)
	return present && strings.TrimSpace(value.text) != ""
}

func foundryDeploymentName(
	c *configuration, property string, defaultName string, enabled bool,
) (string, error) {
	if !enabled {
		return "", nil
	}
	deploymentName := defaultName
	if deployment, present := c.raw(property); present {
		value := strings.TrimSpace(deployment.text)
		if _, fallback, placeholderOnly := runtimePlaceholder(value); placeholderOnly {
			if strings.TrimSpace(fallback) != "" {
				deploymentName = strings.TrimSpace(fallback)
			}
		} else if value != "" {
			deploymentName = value
		}
	}
	if !modelDeploymentName.MatchString(deploymentName) {
		return "", invalidConfiguration(
			"%s must contain 2-64 letters, numbers, periods, underscores, or hyphens",
			property)
	}
	return deploymentName, nil
}

func validateResourceLayout(m *model, projectDirectory string) error {
	for _, resource := range m.Build.Resources {
		if strings.Contains(resource.Directory, "${") || strings.Contains(resource.TargetPath, "${") {
			return unsupportedConfiguration("Maven build.resources must be fully resolved")
		}
		directory := resource.Directory
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(projectDirectory, directory)
		}
		relative, err := filepath.Rel(projectDirectory, directory)
		if err != nil || filepath.ToSlash(relative) != "src/main/resources" ||
			strings.TrimSpace(resource.TargetPath) != "" {
			return unsupportedConfiguration(
				"custom Maven build.resources are not yet supported; application configuration must use " +
					"src/main/resources without a targetPath")
		}
	}
	if slices.ContainsFunc(m.Build.Resources, func(resource buildResource) bool {
		return strings.EqualFold(strings.TrimSpace(resource.Filtering), "true")
	}) && resourceFilteringExpandsRuntimePlaceholders(m.Build.Plugins) {
		return unsupportedConfiguration(
			"Maven resource filtering with ${...} delimiters is not supported because it can embed runtime credentials")
	}
	return nil
}

func resourceFilteringExpandsRuntimePlaceholders(plugins []buildPlugin) bool {
	var configurations []pluginConfiguration
	for _, plugin := range plugins {
		if plugin.Artifact != "maven-resources-plugin" ||
			plugin.Group != "" && plugin.Group != "org.apache.maven.plugins" {
			continue
		}
		for _, execution := range plugin.Executions {
			if slices.Contains(execution.Goals, "resources") {
				configurations = append(configurations,
					execution.Configuration.withDefaults(plugin.Configuration))
			}
		}
		if len(configurations) == 0 {
			configurations = append(configurations, plugin.Configuration)
		}
	}
	if len(configurations) == 0 {
		return true
	}
	return slices.ContainsFunc(configurations, func(configuration pluginConfiguration) bool {
		if !strings.EqualFold(strings.TrimSpace(configuration.UseDefaultDelimiters), "false") {
			return true
		}
		return slices.ContainsFunc(configuration.Delimiters, func(delimiter string) bool {
			return strings.Contains(delimiter, "$")
		})
	})
}

func inspectCode(ctx context.Context, root *os.Root) error {
	return fs.WalkDir(root.FS(), "src/main/java", func(file string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspecting application code: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || path.Ext(file) != ".java" {
			return nil
		}
		data, err := root.ReadFile(file)
		if err != nil {
			return fmt.Errorf("reading application source: %w", err)
		}
		code := javaCommentsAndLiterals.ReplaceAll(data, nil)
		if customDatasource.Match(code) {
			return unsupportedConfiguration("custom datasource construction in %s requires explicit binding analysis", file)
		}
		if runtimeActivation.Match(code) || builderProfileActivation.Match(code) {
			return unsupportedConfiguration(
				"programmatic profile activation in %s cannot be inferred from configuration", file)
		}
		return nil
	})
}

func detectDatasource(m *model, c *configuration) (*Requirement, bool, error) {
	for _, setting := range []struct {
		key         string
		replacement string
	}{
		{"spring.datasource.hikari.username", "spring.datasource.username/password"},
		{"spring.datasource.hikari.password", "spring.datasource.username/password"},
		{"spring.datasource.hikari.jdbc-url", "spring.datasource.url"},
		{"spring.datasource.hikari.data-source-class-name", "spring.datasource.driver-class-name"},
		{"spring.datasource.hikari.driver-class-name", "spring.datasource.driver-class-name"},
		{"spring.datasource.tomcat.url", "spring.datasource.url"},
		{"spring.datasource.tomcat.username", "spring.datasource.username"},
		{"spring.datasource.tomcat.password", "spring.datasource.password"},
		{"spring.datasource.tomcat.driver-class-name", "spring.datasource.driver-class-name"},
		{"spring.datasource.dbcp2.url", "spring.datasource.url"},
		{"spring.datasource.dbcp2.username", "spring.datasource.username"},
		{"spring.datasource.dbcp2.password", "spring.datasource.password"},
		{"spring.datasource.dbcp2.driver-class-name", "spring.datasource.driver-class-name"},
	} {
		if _, present := c.raw(setting.key); present {
			return nil, false, unsupportedConfiguration(
				"%s is not supported; use %s instead", setting.key, setting.replacement)
		}
	}
	for _, key := range []string{"spring.datasource.jndi-name"} {
		value, err := c.resolve(key)
		if err != nil {
			return nil, false, err
		}
		if value != "" {
			return nil, false, unsupportedConfiguration("%s requires custom datasource binding analysis", key)
		}
	}
	authentication := "password"
	for _, key := range []string{"spring.datasource.azure.passwordless-enabled",
		"spring.cloud.azure.jdbc.passwordless-enabled"} {
		value, err := c.resolve(key)
		if err != nil {
			return nil, false, err
		}
		if value != "" && !strings.EqualFold(value, "true") && !strings.EqualFold(value, "false") {
			return nil, false, invalidConfiguration("%s must be true or false", key)
		}
		if strings.EqualFold(value, "true") {
			authentication = "managedIdentity"
		}
	}
	jdbc, err := c.resolve("spring.datasource.url")
	if err != nil {
		return nil, false, err
	}
	excluded, err := c.resolve("spring.autoconfigure.exclude")
	if err != nil {
		return nil, false, err
	}
	if slices.ContainsFunc(strings.Split(excluded, ","), func(value string) bool {
		return strings.TrimSpace(value) ==
			"org.springframework.boot.autoconfigure.jdbc.DataSourceAutoConfiguration"
	}) {
		if jdbc != "" {
			return nil, false, invalidConfiguration(
				"datasource auto-configuration is disabled; cannot infer its runtime binding")
		}
		return nil, false, nil
	}
	embeddedDrivers := [][2]string{
		{"com.h2database", "h2"}, {"org.apache.derby", "derby"}, {"org.hsqldb", "hsqldb"},
	}
	if jdbc == "" {
		if authentication == "managedIdentity" {
			return nil, false, invalidConfiguration(
				"passwordless authentication requires an active PostgreSQL datasource URL")
		}
		for _, driver := range embeddedDrivers {
			if m.hasDependency(driver[0], driver[1]) {
				return nil, true, nil
			}
		}
		if m.hasDependency("org.postgresql", "postgresql") || mysqlDriver(m) {
			return nil, false, unsupportedConfiguration(
				"external JDBC drivers are available, but no active datasource URL is resolved; " +
					"configure spring.datasource.url or select --profiles explicitly")
		}
		return nil, false, nil
	}
	for i, engine := range []string{"h2", "derby", "hsqldb"} {
		if strings.HasPrefix(jdbc, "jdbc:"+engine+":") {
			if authentication == "managedIdentity" {
				return nil, false, invalidConfiguration(
					"passwordless authentication cannot be used with an embedded datasource")
			}
			if !m.hasDependency(embeddedDrivers[i][0], embeddedDrivers[i][1]) {
				return nil, false, invalidConfiguration("the active embedded datasource has no packaged JDBC driver")
			}
			memoryPrefix := "jdbc:" + engine + ":mem:"
			if engine == "derby" {
				memoryPrefix = "jdbc:derby:memory:"
			}
			if !strings.HasPrefix(jdbc, memoryPrefix) {
				return nil, false, unsupportedConfiguration(
					"non-memory embedded databases need a persistence policy not yet supported")
			}
			return nil, true, nil
		}
	}
	var kind ResourceKind
	switch {
	case strings.HasPrefix(jdbc, "jdbc:mysql://"):
		kind = MySQL
		if !mysqlDriver(m) {
			return nil, false, invalidConfiguration("the active MySQL datasource has no runtime MySQL JDBC driver")
		}
	case strings.HasPrefix(jdbc, "jdbc:postgresql://"):
		kind = PostgreSQL
		if !m.hasDependency("org.postgresql", "postgresql") {
			return nil, false, invalidConfiguration("the active PostgreSQL datasource has no runtime PostgreSQL JDBC driver")
		}
	default:
		return nil, false, unsupportedConfiguration("the active JDBC connection type is not yet supported")
	}
	driverClass, err := c.resolve("spring.datasource.driver-class-name")
	if err != nil {
		return nil, false, err
	}
	if err := validateExternalDriver(kind, driverClass); err != nil {
		return nil, false, err
	}
	u, err := url.Parse(strings.TrimPrefix(jdbc, "jdbc:"))
	if err != nil || u.Hostname() == "" || strings.Contains(u.Host, ",") || u.Fragment != "" {
		return nil, false, invalidConfiguration("a single-host JDBC URL with an explicit database name is required")
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !databaseName.MatchString(name) {
		return nil, false, invalidConfiguration(
			"database name must start with a letter and contain 1-63 letters, digits, or underscores")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, false, invalidConfiguration("the JDBC query string is invalid")
	}
	if plugins, present := query["authenticationPluginClassName"]; present {
		if kind != PostgreSQL ||
			!slices.Equal(plugins, []string{
				"com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin",
			}) {
			return nil, false, unsupportedConfiguration("the JDBC authentication plugin is not supported")
		}
		authentication = "managedIdentity"
		query.Del("authenticationPluginClassName")
	}
	if values, present := query["azure.managedIdentityEnabled"]; present {
		if !slices.Equal(values, []string{"true"}) || authentication != "managedIdentity" {
			return nil, false, invalidConfiguration(
				"azure.managedIdentityEnabled requires the supported PostgreSQL token plugin")
		}
		query.Del("azure.managedIdentityEnabled")
	}
	if authentication == "managedIdentity" && kind == MySQL {
		return nil, false, unsupportedConfiguration(
			"MySQL managed identity is deferred pending live configuration validation")
	}
	options, err := jdbcOptions(kind, query.Encode())
	if err != nil {
		return nil, false, err
	}
	source, _ := c.raw("spring.datasource.url")
	trace := slices.Clone(c.traces["spring.datasource.url"])
	for _, evidence := range c.traces["spring.datasource.driver-class-name"] {
		if !slices.Contains(trace, evidence) {
			trace = append(trace, evidence)
		}
	}
	return &Requirement{
		Kind: kind, Name: name, Authentication: authentication, JDBCOptions: options, Driver: driverClass,
		Evidence: Evidence{Property: "spring.datasource.url", Source: source.source},
		Trace:    trace,
	}, false, nil
}

func validateExternalDriver(kind ResourceKind, driver string) error {
	if driver == "" || kind == PostgreSQL && driver == "org.postgresql.Driver" ||
		kind == MySQL && slices.Contains([]string{"com.mysql.cj.jdbc.Driver", "com.mysql.jdbc.Driver"}, driver) {
		return nil
	}
	return invalidConfiguration("the configured JDBC driver does not match the supported datasource binding")
}

func managedIdentityOptions(options string) (string, error) {
	values, err := url.ParseQuery(options)
	if err != nil {
		return "", errors.New("the JDBC query string is invalid")
	}
	values.Set("sslmode", "verify-full")
	values.Set("sslrootcert", "/etc/ssl/certs/ca-certificates.crt")
	values.Set("authenticationPluginClassName",
		"com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin")
	values.Set("azure.managedIdentityEnabled", "true")
	return values.Encode(), nil
}

func mysqlDriver(m *model) bool {
	return m.hasDependency("com.mysql", "mysql-connector-j") || m.hasDependency("mysql", "mysql-connector-java")
}

func jdbcOptions(kind ResourceKind, query string) (string, error) {
	values, err := url.ParseQuery(query)
	if err != nil {
		return "", invalidConfiguration("the JDBC query string is invalid")
	}
	allowed := map[string]bool{
		"sslmode": true, "sslMode": true, "useSSL": true, "requireSSL": true, "verifyServerCertificate": true,
		"connectTimeout": true, "socketTimeout": true, "tcpKeepAlive": true, "ApplicationName": true,
		"serverTimezone": true, "useUnicode": true, "characterEncoding": true, "allowPublicKeyRetrieval": true,
	}
	for key := range values {
		if !allowed[key] {
			return "", unsupportedConfiguration(
				"the JDBC URL contains an option whose deployment binding is not yet supported")
		}
		if (key == "sslmode" || key == "sslMode" || key == "verifyServerCertificate") && len(values[key]) != 1 {
			return "", invalidConfiguration("%s must be specified once", key)
		}
	}
	if kind == PostgreSQL {
		mode := strings.ToLower(values.Get("sslmode"))
		switch mode {
		case "", "disable", "allow", "prefer", "require":
			mode = "require"
		case "verify-ca", "verify-full":
			values.Set("sslrootcert", "/etc/ssl/certs/ca-certificates.crt")
		default:
			return "", invalidConfiguration("sslmode is not supported")
		}
		values.Set("sslmode", mode)
	} else {
		mode := strings.ToUpper(values.Get("sslMode"))
		if mode == "" {
			mode = "REQUIRED"
			if verification, present := values["verifyServerCertificate"]; present {
				switch strings.ToLower(verification[0]) {
				case "true", "yes":
					mode = "VERIFY_CA"
				case "false", "no":
				default:
					return "", invalidConfiguration("verifyServerCertificate must be true, false, yes, or no")
				}
			}
		}
		switch mode {
		case "DISABLED", "PREFERRED":
			mode = "REQUIRED"
		case "REQUIRED", "VERIFY_CA", "VERIFY_IDENTITY":
		default:
			return "", invalidConfiguration("sslMode is not supported")
		}
		values.Set("sslMode", mode)
		// Keep encryption and certificate verification enabled for legacy drivers.
		values.Set("useSSL", "true")
		values.Set("requireSSL", "true")
		verification := "false"
		if mode == "VERIFY_CA" || mode == "VERIFY_IDENTITY" {
			verification = "true"
		}
		values.Set("verifyServerCertificate", verification)
	}
	return values.Encode(), nil
}
