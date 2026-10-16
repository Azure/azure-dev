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
	MavenWrapper           bool
	ExternalConfigurations []string
	IdentityExtensions     bool
	InputEvidence          []Evidence
}

var databaseName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)
var customDatasource = regexp.MustCompile(
	`(?s)@(?:org\.springframework\.context\.annotation\.)?Bean\b(?:\s*\([^()]*\))?` +
		`(?:[^{};@]|@[\w.$]+(?:\s*\([^()]*\))?)*?\b(?:DataSource|ConnectionFactory)\s+[\w$]+\s*\(` +
		`|new\s+(?:[\w$]+\.)*(?:HikariDataSource|DriverManagerDataSource)\s*\(`)
var runtimeActivation = regexp.MustCompile(`\.(?:setAdditionalProfiles|setActiveProfiles|setDefaultProfiles)\s*\(`)
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
	m, java, err := parseModel(data)
	if err != nil {
		return nil, err
	}
	projectDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolving application directory: %w", err)
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
	return a, nil
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
		if runtimeActivation.Match(code) {
			return unsupportedConfiguration(
				"programmatic profile activation in %s cannot be inferred from configuration", file)
		}
		return nil
	})
}

func detectDatasource(m *model, c *configuration) (*Requirement, bool, error) {
	for _, key := range []string{"spring.datasource.hikari.username", "spring.datasource.hikari.password"} {
		if _, present := c.raw(key); present {
			return nil, false, unsupportedConfiguration(
				"%s is not supported; use spring.datasource.username/password instead", key)
		}
	}
	for _, key := range []string{"spring.datasource.hikari.jdbc-url", "spring.datasource.hikari.data-source-class-name",
		"spring.datasource.jndi-name"} {
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
	if strings.Contains(excluded, "DataSourceAutoConfiguration") {
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
	values.Set("authenticationPluginClassName",
		"com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin")
	values.Set("sslrootcert", "/etc/ssl/certs/ca-certificates.crt")
	values.Set("azure.managedIdentityEnabled", "true")
	return values.Encode(), nil
}

// SetAuthentication applies an explicit deployment choice without downgrading inferred token authentication.
func SetAuthentication(application *Application, authentication string) error {
	if authentication != "password" && authentication != "managedIdentity" {
		return errors.New("--database-auth must be password or managedIdentity")
	}
	if len(application.Requirements) != 1 {
		return errors.New("--database-auth requires an active external datasource")
	}
	requirement := &application.Requirements[0]
	if requirement.Authentication == "managedIdentity" && authentication == "password" {
		return errors.New("--database-auth=password conflicts with the application's passwordless configuration")
	}
	if requirement.Kind == MySQL && authentication == "managedIdentity" {
		return errors.New("MySQL managed identity is deferred pending live configuration validation")
	}
	requirement.Authentication = authentication
	return nil
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
