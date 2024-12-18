package appdetect

import (
	"fmt"
	"log"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type SpringBootProject struct {
	springBootVersion     string // todo: delete this, because it's only used once.
	applicationProperties map[string]string
	pom                   pom
}

type DatabaseDependencyRule struct {
	databaseDep       DatabaseDep
	mavenDependencies []MavenDependency
}

type MavenDependency struct {
	groupId    string
	artifactId string
}

var databaseDependencyRules = []DatabaseDependencyRule{
	{
		databaseDep: DbPostgres,
		mavenDependencies: []MavenDependency{
			{
				groupId:    "org.postgresql",
				artifactId: "postgresql",
			},
			{
				groupId:    "com.azure.spring",
				artifactId: "spring-cloud-azure-starter-jdbc-postgresql",
			},
		},
	},
	{
		databaseDep: DbMySql,
		mavenDependencies: []MavenDependency{
			{
				groupId:    "com.mysql",
				artifactId: "mysql-connector-j",
			},
			{
				groupId:    "com.azure.spring",
				artifactId: "spring-cloud-azure-starter-jdbc-mysql",
			},
		},
	},
	{
		databaseDep: DbRedis,
		mavenDependencies: []MavenDependency{
			{
				groupId:    "org.springframework.boot",
				artifactId: "spring-boot-starter-data-redis",
			},
			{
				groupId:    "org.springframework.boot",
				artifactId: "spring-boot-starter-data-redis-reactive",
			},
		},
	},
	{
		databaseDep: DbMongo,
		mavenDependencies: []MavenDependency{
			{
				groupId:    "org.springframework.boot",
				artifactId: "spring-boot-starter-data-mongodb",
			},
			{
				groupId:    "org.springframework.boot",
				artifactId: "spring-boot-starter-data-mongodb-reactive",
			},
		},
	},
	{
		databaseDep: DbCosmos,
		mavenDependencies: []MavenDependency{
			{
				groupId:    "com.azure.spring",
				artifactId: "spring-cloud-azure-starter-data-cosmos",
			},
		},
	},
}

// todo: remove parentPom, when passed in the pom is the effective pom.
func detectAzureDependenciesByAnalyzingSpringBootProject(parentPom *pom, currentPom *pom, azdProject *Project) {
	effectivePom, err := toEffectivePom(filepath.Join(currentPom.path, "pom.xml"))
	if err == nil {
		currentPom = &effectivePom
	}
	if !isSpringBootApplication(currentPom) {
		log.Printf("Skip analyzing spring boot project. path = %s.", currentPom.path)
		return
	}
	var springBootProject = SpringBootProject{
		springBootVersion:     detectSpringBootVersion(parentPom, currentPom),
		applicationProperties: readProperties(azdProject.Path),
		pom:                   *currentPom,
	}
	detectDatabases(azdProject, &springBootProject)
	detectServiceBus(azdProject, &springBootProject)
	detectEventHubs(azdProject, &springBootProject)
	detectStorageAccount(azdProject, &springBootProject)
	detectMetadata(azdProject, &springBootProject)
	detectSpringFrontend(azdProject, &springBootProject)
}

func detectSpringFrontend(azdProject *Project, springBootProject *SpringBootProject) {
	for _, p := range springBootProject.pom.Build.Plugins {
		if p.GroupId == "com.github.eirslett" && p.ArtifactId == "frontend-maven-plugin" {
			azdProject.Dependencies = append(azdProject.Dependencies, SpringFrontend)
			break
		}
	}
}

func detectDatabases(azdProject *Project, springBootProject *SpringBootProject) {
	databaseDepMap := map[DatabaseDep]struct{}{}
	for _, rule := range databaseDependencyRules {
		for _, targetDependency := range rule.mavenDependencies {
			var targetGroupId = targetDependency.groupId
			var targetArtifactId = targetDependency.artifactId
			if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
				databaseDepMap[rule.databaseDep] = struct{}{}
				logServiceAddedAccordingToMavenDependency(rule.databaseDep.Display(),
					targetGroupId, targetArtifactId)
				break
			}
		}
	}
	if len(databaseDepMap) > 0 {
		azdProject.DatabaseDeps = slices.SortedFunc(maps.Keys(databaseDepMap),
			func(a, b DatabaseDep) int {
				return strings.Compare(string(a), string(b))
			})
	}
}

func detectServiceBus(azdProject *Project, springBootProject *SpringBootProject) {
	// we need to figure out multiple projects are using the same service bus
	detectServiceBusAccordingToJMSMavenDependency(azdProject, springBootProject)
	detectServiceBusAccordingToSpringCloudStreamBinderMavenDependency(azdProject, springBootProject)
}

func detectServiceBusAccordingToJMSMavenDependency(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-servicebus-jms"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		newDependency := AzureDepServiceBus{
			IsJms: true,
		}
		azdProject.AzureDeps = append(azdProject.AzureDeps, newDependency)
		logServiceAddedAccordingToMavenDependency(newDependency.ResourceDisplay(), targetGroupId, targetArtifactId)
	}
}

func detectServiceBusAccordingToSpringCloudStreamBinderMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-stream-binder-servicebus"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		bindingDestinations := getBindingDestinationMap(springBootProject.applicationProperties)
		var destinations = DistinctValues(bindingDestinations)
		newDep := AzureDepServiceBus{
			Queues: destinations,
			IsJms:  false,
		}
		azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for bindingName, destination := range bindingDestinations {
			log.Printf("  Detected Service Bus queue [%s] for binding [%s] by analyzing property file.",
				destination, bindingName)
		}
	}
}

func detectEventHubs(azdProject *Project, springBootProject *SpringBootProject) {
	// we need to figure out multiple projects are using the same event hub
	detectEventHubsAccordingToSpringCloudStreamBinderMavenDependency(azdProject, springBootProject)
	detectEventHubsAccordingToSpringCloudEventhubsStarterDependency(azdProject, springBootProject)
	detectEventHubsAccordingToSpringCloudStreamKafkaMavenDependency(azdProject, springBootProject)
}

func detectEventHubsAccordingToSpringCloudStreamBinderMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-stream-binder-eventhubs"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		bindingDestinations := getBindingDestinationMap(springBootProject.applicationProperties)
		newDep := AzureDepEventHubs{
			EventHubsNamePropertyMap: bindingDestinations,
			UseKafka:                 false,
		}
		azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for bindingName, destination := range bindingDestinations {
			log.Printf("  Detected Event Hub [%s] for binding [%s] by analyzing property file.",
				destination, bindingName)
		}
	}
}

func detectEventHubsAccordingToSpringCloudEventhubsStarterDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-eventhubs"
	var targetPropertyName = "spring.cloud.azure.eventhubs.event-hub-name"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		eventHubsNamePropertyMap := map[string]string{
			targetPropertyName: springBootProject.applicationProperties[targetPropertyName],
		}
		newDep := AzureDepEventHubs{
			EventHubsNamePropertyMap: eventHubsNamePropertyMap,
			UseKafka:                 false,
		}
		azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for property, name := range eventHubsNamePropertyMap {
			log.Printf("  Detected Event Hub [%s] for [%s] by analyzing property file.", property, name)
		}
	}
}

func detectEventHubsAccordingToSpringCloudStreamKafkaMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "org.springframework.cloud"
	var targetArtifactId = "spring-cloud-starter-stream-kafka"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		bindingDestinations := getBindingDestinationMap(springBootProject.applicationProperties)
		newDep := AzureDepEventHubs{
			EventHubsNamePropertyMap: bindingDestinations,
			UseKafka:                 true,
			SpringBootVersion:        springBootProject.springBootVersion,
		}
		azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for bindingName, destination := range bindingDestinations {
			log.Printf("  Detected Kafka Topic [%s] for binding [%s] by analyzing property file.",
				destination, bindingName)
		}
	}
}

func detectStorageAccount(azdProject *Project, springBootProject *SpringBootProject) {
	detectStorageAccountAccordingToSpringCloudStreamBinderMavenDependencyAndProperty(azdProject, springBootProject)
}

func detectStorageAccountAccordingToSpringCloudStreamBinderMavenDependencyAndProperty(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-stream-binder-eventhubs"
	var targetPropertyName = "spring.cloud.azure.eventhubs.processor.checkpoint-store.container-name"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		bindingDestinations := getBindingDestinationMap(springBootProject.applicationProperties)
		containsInBindingName := ""
		for bindingName := range bindingDestinations {
			if strings.Contains(bindingName, "-in-") { // Example: consume-in-0
				containsInBindingName = bindingName
				break
			}
		}
		if containsInBindingName != "" {
			containerNamePropertyMap := make(map[string]string)
			for key, value := range springBootProject.applicationProperties {
				if strings.HasSuffix(key, targetPropertyName) {
					containerNamePropertyMap[key] = value
				}
			}
			newDep := AzureDepStorageAccount{
				ContainerNamePropertyMap: containerNamePropertyMap,
			}
			azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
			logServiceAddedAccordingToMavenDependencyAndExtraCondition(newDep.ResourceDisplay(), targetGroupId,
				targetArtifactId, "binding name ["+containsInBindingName+"] contains '-in-'")
			for property, containerName := range containerNamePropertyMap {
				log.Printf("  Detected Storage container name: [%s] for [%s] by analyzing property file.",
					containerName, property)
			}
		}
	}
}

func detectMetadata(azdProject *Project, springBootProject *SpringBootProject) {
	detectPropertySpringApplicationName(azdProject, springBootProject)
	detectPropertySpringCloudAzureCosmosDatabase(azdProject, springBootProject)
	detectPropertySpringDataMongodbDatabase(azdProject, springBootProject)
	detectPropertySpringDataMongodbUri(azdProject, springBootProject)
	detectPropertySpringDatasourceUrl(azdProject, springBootProject)

	detectDependencySpringCloudAzureStarter(azdProject, springBootProject)
	detectDependencySpringCloudAzureStarterJdbcMysql(azdProject, springBootProject)
	detectDependencySpringCloudAzureStarterJdbcPostgresql(azdProject, springBootProject)
	detectDependencySpringCloudConfig(azdProject, springBootProject)
	detectDependencySpringCloudEureka(azdProject, springBootProject)
}

func detectPropertySpringCloudAzureCosmosDatabase(azdProject *Project, springBootProject *SpringBootProject) {
	var targetPropertyName = "spring.cloud.azure.cosmos.database"
	propertyValue, ok := springBootProject.applicationProperties[targetPropertyName]
	if !ok {
		log.Printf("%s property not exist in project. Path = %s", targetPropertyName, azdProject.Path)
		return
	}
	databaseName := ""
	if IsValidDatabaseName(propertyValue) {
		databaseName = propertyValue
	} else {
		return
	}
	if azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl == nil {
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl = map[DatabaseDep]string{}
	}
	if azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbCosmos] == "" {
		// spring.data.mongodb.database has lower priority than spring.data.mongodb.uri
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbCosmos] = databaseName
	}
}

func detectPropertySpringDatasourceUrl(azdProject *Project, springBootProject *SpringBootProject) {
	var targetPropertyName = "spring.datasource.url"
	propertyValue, ok := springBootProject.applicationProperties[targetPropertyName]
	if !ok {
		log.Printf("%s property not exist in project. Path = %s", targetPropertyName, azdProject.Path)
		return
	}
	databaseName := getDatabaseName(propertyValue)
	if databaseName == "" {
		log.Printf("can not get database name from property: %s", targetPropertyName)
		return
	}
	if azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl == nil {
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl = map[DatabaseDep]string{}
	}
	if strings.HasPrefix(propertyValue, "jdbc:postgresql") {
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbPostgres] = databaseName
	} else if strings.HasPrefix(propertyValue, "jdbc:mysql") {
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbMySql] = databaseName
	}
}

func detectPropertySpringDataMongodbUri(azdProject *Project, springBootProject *SpringBootProject) {
	var targetPropertyName = "spring.data.mongodb.uri"
	propertyValue, ok := springBootProject.applicationProperties[targetPropertyName]
	if !ok {
		log.Printf("%s property not exist in project. Path = %s", targetPropertyName, azdProject.Path)
		return
	}
	databaseName := getDatabaseName(propertyValue)
	if databaseName == "" {
		log.Printf("can not get database name from property: %s", targetPropertyName)
		return
	}
	if azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl == nil {
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl = map[DatabaseDep]string{}
	}
	azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbMongo] = databaseName
}

func detectPropertySpringDataMongodbDatabase(azdProject *Project, springBootProject *SpringBootProject) {
	var targetPropertyName = "spring.data.mongodb.database"
	propertyValue, ok := springBootProject.applicationProperties[targetPropertyName]
	if !ok {
		log.Printf("%s property not exist in project. Path = %s", targetPropertyName, azdProject.Path)
		return
	}
	databaseName := ""
	if IsValidDatabaseName(propertyValue) {
		databaseName = propertyValue
	} else {
		return
	}
	if azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl == nil {
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl = map[DatabaseDep]string{}
	}
	if azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbMongo] == "" {
		// spring.data.mongodb.database has lower priority than spring.data.mongodb.uri
		azdProject.Metadata.DatabaseNameInPropertySpringDatasourceUrl[DbMongo] = databaseName
	}
}

func getDatabaseName(datasourceURL string) string {
	lastSlashIndex := strings.LastIndex(datasourceURL, "/")
	if lastSlashIndex == -1 {
		return ""
	}
	result := datasourceURL[lastSlashIndex+1:]
	if idx := strings.Index(result, "?"); idx != -1 {
		result = result[:idx]
	}
	if IsValidDatabaseName(result) {
		return result
	}
	return ""
}

func IsValidDatabaseName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	re := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	return re.MatchString(name)
}

func detectDependencySpringCloudAzureStarter(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudAzureStarter = true
		logMetadataUpdated("ContainsDependencySpringCloudAzureStarter = true")
	}
}

func detectDependencySpringCloudAzureStarterJdbcPostgresql(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-jdbc-postgresql"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudAzureStarterJdbcPostgresql = true
		logMetadataUpdated("ContainsDependencySpringCloudAzureStarterJdbcPostgresql = true")
	}
}

func detectDependencySpringCloudAzureStarterJdbcMysql(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-jdbc-mysql"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudAzureStarterJdbcMysql = true
		logMetadataUpdated("ContainsDependencySpringCloudAzureStarterJdbcMysql = true")
	}
}

func detectPropertySpringApplicationName(azdProject *Project, springBootProject *SpringBootProject) {
	var targetPropertyName = "spring.application.name"
	if appName, ok := springBootProject.applicationProperties[targetPropertyName]; ok {
		azdProject.Metadata.ApplicationName = appName
	}
}

func detectDependencySpringCloudEureka(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "org.springframework.cloud"
	var targetArtifactId = "spring-cloud-starter-netflix-eureka-server"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudEurekaServer = true
		logMetadataUpdated("ContainsDependencySpringCloudEurekaServer = true")
	}

	targetGroupId = "org.springframework.cloud"
	targetArtifactId = "spring-cloud-starter-netflix-eureka-client"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudEurekaClient = true
		logMetadataUpdated("ContainsDependencySpringCloudEurekaClient = true")
	}
}

func detectDependencySpringCloudConfig(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "org.springframework.cloud"
	var targetArtifactId = "spring-cloud-config-server"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudConfigServer = true
		logMetadataUpdated("ContainsDependencySpringCloudConfigServer = true")
	}

	targetGroupId = "org.springframework.cloud"
	targetArtifactId = "spring-cloud-starter-config"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		azdProject.Metadata.ContainsDependencySpringCloudConfigClient = true
		logMetadataUpdated("ContainsDependencySpringCloudConfigClient = true")
	}
}

func logServiceAddedAccordingToMavenDependency(resourceName, groupId string, artifactId string) {
	logServiceAddedAccordingToMavenDependencyAndExtraCondition(resourceName, groupId, artifactId, "")
}

func logServiceAddedAccordingToMavenDependencyAndExtraCondition(
	resourceName, groupId string, artifactId string, extraCondition string) {
	insertedString := ""
	extraCondition = strings.TrimSpace(extraCondition)
	if extraCondition != "" {
		insertedString = " and " + extraCondition
	}
	log.Printf("Detected '%s' because found dependency '%s:%s' in pom.xml file%s.",
		resourceName, groupId, artifactId, insertedString)
}

func logMetadataUpdated(info string) {
	log.Printf("Metadata updated. %s.", info)
}

func detectSpringBootVersion(parentPom *pom, currentPom *pom) string {
	// currentPom prioritize than parentPom
	if currentPom != nil {
		if version := detectSpringBootVersionFromPom(currentPom); version != UnknownSpringBootVersion {
			return version
		}
	}
	// fallback to detect parentPom
	if parentPom != nil {
		return detectSpringBootVersionFromPom(parentPom)
	}
	return UnknownSpringBootVersion
}

func detectSpringBootVersionFromPom(pom *pom) string {
	if pom.Parent.ArtifactId == "spring-boot-starter-parent" {
		return pom.Parent.Version
	} else {
		for _, dep := range pom.DependencyManagement.Dependencies {
			if dep.ArtifactId == "spring-boot-dependencies" {
				return dep.Version
			}
		}
		for _, dep := range pom.Dependencies {
			if dep.GroupId == "org.springframework.boot" {
				return dep.Version
			}
		}
	}
	return UnknownSpringBootVersion
}

func isSpringBootApplication(pom *pom) bool {
	// how can we tell it's a Spring Boot project?
	// 1. It has a parent with a groupId of org.springframework.boot and an artifactId of spring-boot-starter-parent
	// 2. It has a dependency management with a groupId of org.springframework.boot and an artifactId of
	// spring-boot-dependencies
	// 3. It has a dependency with a groupId of org.springframework.boot and an artifactId that starts with
	// spring-boot-starter
	if pom.Parent.GroupId == "org.springframework.boot" &&
		pom.Parent.ArtifactId == "spring-boot-starter-parent" {
		return true
	}
	for _, dep := range pom.DependencyManagement.Dependencies {
		if dep.GroupId == "org.springframework.boot" &&
			dep.ArtifactId == "spring-boot-dependencies" {
			return true
		}
	}
	for _, dep := range pom.Dependencies {
		if dep.GroupId == "org.springframework.boot" &&
			strings.HasPrefix(dep.ArtifactId, "spring-boot-starter") { // maybe delete condition of this line
			return true
		}
	}
	for _, dep := range pom.Build.Plugins {
		if dep.GroupId == "org.springframework.boot" &&
			dep.ArtifactId == "spring-boot-maven-plugin" {
			return true
		}
	}
	return false
}

func DistinctValues(input map[string]string) []string {
	valueSet := make(map[string]struct{})
	for _, value := range input {
		valueSet[value] = struct{}{}
	}

	var result []string
	for value := range valueSet {
		result = append(result, value)
	}

	return result
}

// Function to find all properties that match the pattern `spring.cloud.stream.bindings.<binding-name>.destination`
func getBindingDestinationMap(properties map[string]string) map[string]string {
	result := make(map[string]string)

	// Iterate through the properties map and look for matching keys
	for key, value := range properties {
		// Check if the key matches the pattern `spring.cloud.stream.bindings.<binding-name>.destination`
		if strings.HasPrefix(key, "spring.cloud.stream.bindings.") && strings.HasSuffix(key, ".destination") {
			// Store the binding name and destination value
			result[key] = fmt.Sprintf("%v", value)
		}
	}

	return result
}

func hasDependency(project *SpringBootProject, groupId string, artifactId string) bool {
	for _, projectDependency := range project.pom.Dependencies {
		if projectDependency.GroupId == groupId && projectDependency.ArtifactId == artifactId {
			return true
		}
	}
	return false
}
