package appdetect

import (
	"fmt"
	"log"
	"maps"
	"regexp"
	"slices"
	"strings"
)

type SpringBootProject struct {
	applicationProperties map[string]string
	pom                   pom
}

const UnknownSpringBootVersion string = "unknownSpringBootVersion"

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

func detectAzureDependenciesByAnalyzingSpringBootProject(mavenProject mavenProject, azdProject *Project) {
	pom := mavenProject.pom
	if !isSpringBootApplication(pom) {
		log.Printf("Skip analyzing spring boot project. pomFilePath = %s.", pom.pomFilePath)
		return
	}
	var springBootProject = SpringBootProject{
		applicationProperties: readProperties(azdProject.Path),
		pom:                   pom,
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
	detectEventHubsAccordingToSpringCloudEventhubsStarterMavenDependency(azdProject, springBootProject)
	detectEventHubsAccordingToSpringIntegrationEventhubsMavenDependency(azdProject, springBootProject)
	detectEventHubsAccordingToSpringMessagingEventhubsMavenDependency(azdProject, springBootProject)
	detectEventHubsAccordingToSpringCloudStreamKafkaMavenDependency(azdProject, springBootProject)
	detectEventHubsAccordingToSpringKafkaMavenDependency(azdProject, springBootProject)
}

func detectEventHubsAccordingToSpringCloudStreamBinderMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-stream-binder-eventhubs"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		bindingDestinations := getBindingDestinationMap(springBootProject.applicationProperties)
		newDep := AzureDepEventHubs{
			EventHubsNamePropertyMap: bindingDestinations,
			DependencyTypes:          []DependencyType{SpringCloudStreamEventHubs},
		}
		addAzureDepEventHubsIntoProject(azdProject, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for bindingName, destination := range bindingDestinations {
			log.Printf("  Detected Event Hub [%s] for binding [%s] by analyzing property file.",
				destination, bindingName)
		}
	}
}

func detectEventHubsAccordingToSpringCloudEventhubsStarterMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-eventhubs"
	// event-hub-name can be specified in different levels, see
	// https://learn.microsoft.com/azure/developer/java/spring-framework/configuration-properties-azure-event-hubs
	var targetPropertyNames = []string{
		"spring.cloud.azure.eventhubs.event-hub-name",
		"spring.cloud.azure.eventhubs.producer.event-hub-name",
		"spring.cloud.azure.eventhubs.consumer.event-hub-name",
		"spring.cloud.azure.eventhubs.processor.event-hub-name",
	}
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		eventHubsNamePropertyMap := map[string]string{}
		for _, propertyName := range targetPropertyNames {
			if propertyValue, ok := springBootProject.applicationProperties[propertyName]; ok {
				eventHubsNamePropertyMap[propertyName] = propertyValue
			}
		}
		newDep := AzureDepEventHubs{
			EventHubsNamePropertyMap: eventHubsNamePropertyMap,
			DependencyTypes:          []DependencyType{SpringCloudEventHubsStarter},
		}
		addAzureDepEventHubsIntoProject(azdProject, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for property, name := range eventHubsNamePropertyMap {
			log.Printf("  Detected Event Hub [%s] for [%s] by analyzing property file.", property, name)
		}
	}
}

func detectEventHubsAccordingToSpringIntegrationEventhubsMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-integration-eventhubs"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		newDep := AzureDepEventHubs{
			// eventhubs name is empty here because no configured property
			EventHubsNamePropertyMap: map[string]string{},
			DependencyTypes:          []DependencyType{SpringIntegrationEventHubs},
		}
		addAzureDepEventHubsIntoProject(azdProject, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
	}
}

func detectEventHubsAccordingToSpringMessagingEventhubsMavenDependency(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-messaging-azure-eventhubs"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		newDep := AzureDepEventHubs{
			// eventhubs name is empty here because no configured property
			EventHubsNamePropertyMap: map[string]string{},
			DependencyTypes:          []DependencyType{SpringMessagingEventHubs},
		}
		addAzureDepEventHubsIntoProject(azdProject, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
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
			SpringBootVersion:        detectSpringBootVersion(springBootProject.pom),
			DependencyTypes:          []DependencyType{SpringCloudStreamKafka},
		}
		addAzureDepEventHubsIntoProject(azdProject, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
		for bindingName, destination := range bindingDestinations {
			log.Printf("  Detected Kafka Topic [%s] for binding [%s] by analyzing property file.",
				destination, bindingName)
		}
	}
}

func detectEventHubsAccordingToSpringKafkaMavenDependency(azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "org.springframework.kafka"
	var targetArtifactId = "spring-kafka"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		newDep := AzureDepEventHubs{
			// eventhubs name is empty here because no configured property
			EventHubsNamePropertyMap: map[string]string{},
			SpringBootVersion:        detectSpringBootVersion(springBootProject.pom),
			DependencyTypes:          []DependencyType{SpringKafka},
		}
		addAzureDepEventHubsIntoProject(azdProject, newDep)
		logServiceAddedAccordingToMavenDependency(newDep.ResourceDisplay(), targetGroupId, targetArtifactId)
	}
}

func addAzureDepEventHubsIntoProject(
	azdProject *Project,
	newDep AzureDepEventHubs) {
	for index, azureDep := range azdProject.AzureDeps {
		if azureDep, ok := azureDep.(AzureDepEventHubs); ok {
			// already have existing dependency
			for property, eventHubsName := range newDep.EventHubsNamePropertyMap {
				azureDep.EventHubsNamePropertyMap[property] = eventHubsName
			}
			azureDep.DependencyTypes = append(azureDep.DependencyTypes, newDep.DependencyTypes...)
			azureDep.SpringBootVersion = newDep.SpringBootVersion
			azdProject.AzureDeps[index] = azureDep
			return
		}
	}

	// add new dependency
	azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
}

func detectStorageAccount(azdProject *Project, springBootProject *SpringBootProject) {
	detectStorageAccountAccordingToSpringCloudStreamBinderMavenDependencyAndProperty(azdProject, springBootProject)
	detectStorageAccountAccordingToSpringIntegrationEventhubsMavenDependencyAndProperty(azdProject, springBootProject)
	detectStorageAccountAccordingToSpringMessagingEventhubsMavenDependencyAndProperty(azdProject, springBootProject)
}

func detectStorageAccountAccordingToSpringCloudStreamBinderMavenDependencyAndProperty(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-stream-binder-eventhubs"
	var targetPropertyNameSuffix = "spring.cloud.azure.eventhubs.processor.checkpoint-store.container-name"
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
			detectStorageAccountAccordingToProperty(azdProject, springBootProject.applicationProperties,
				targetGroupId, targetArtifactId, targetPropertyNameSuffix,
				"binding name ["+containsInBindingName+"] contains '-in-'")
		}
	}
}

func detectStorageAccountAccordingToSpringIntegrationEventhubsMavenDependencyAndProperty(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-cloud-azure-starter-integration-eventhubs"
	var targetPropertyNameSuffix = "spring.cloud.azure.eventhubs.processor.checkpoint-store.container-name"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		detectStorageAccountAccordingToProperty(azdProject, springBootProject.applicationProperties,
			targetGroupId, targetArtifactId, targetPropertyNameSuffix, "")
	}
}

func detectStorageAccountAccordingToSpringMessagingEventhubsMavenDependencyAndProperty(
	azdProject *Project, springBootProject *SpringBootProject) {
	var targetGroupId = "com.azure.spring"
	var targetArtifactId = "spring-messaging-azure-eventhubs"
	var targetPropertyNameSuffix = "spring.cloud.azure.eventhubs.processor.checkpoint-store.container-name"
	if hasDependency(springBootProject, targetGroupId, targetArtifactId) {
		detectStorageAccountAccordingToProperty(azdProject, springBootProject.applicationProperties,
			targetGroupId, targetArtifactId, targetPropertyNameSuffix, "")
	}
}

func detectStorageAccountAccordingToProperty(azdProject *Project, applicationProperties map[string]string,
	targetGroupId string, targetArtifactId string, targetPropertyNameSuffix string, extraCondition string) {
	containerNamePropertyMap := make(map[string]string)
	for key, value := range applicationProperties {
		if strings.HasSuffix(key, targetPropertyNameSuffix) {
			containerNamePropertyMap[key] = value
		}
	}
	if len(containerNamePropertyMap) > 0 {
		newDep := AzureDepStorageAccount{
			ContainerNamePropertyMap: containerNamePropertyMap,
		}
		azdProject.AzureDeps = append(azdProject.AzureDeps, newDep)
		logServiceAddedAccordingToMavenDependencyAndExtraCondition(newDep.ResourceDisplay(), targetGroupId,
			targetArtifactId, extraCondition)
		for property, containerName := range containerNamePropertyMap {
			log.Printf("  Detected Storage container name: [%s] for [%s] by analyzing property file.",
				containerName, property)
		}
	}
}

func detectMetadata(azdProject *Project, springBootProject *SpringBootProject) {
	detectPropertySpringApplicationName(azdProject, springBootProject)
	detectPropertyServerPort(azdProject, springBootProject)
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

func detectPropertyServerPort(azdProject *Project, springBootProject *SpringBootProject) {
	var targetPropertyName = "server.port"
	if serverPort, ok := springBootProject.applicationProperties[targetPropertyName]; ok {
		azdProject.Metadata.ServerPort = serverPort
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

func detectSpringBootVersion(pom pom) string {
	for _, dep := range pom.Dependencies {
		if dep.GroupId == "org.springframework.boot" {
			return dep.Version
		}
	}
	for _, dep := range pom.Build.Plugins {
		if dep.GroupId == "org.springframework.boot" {
			return dep.Version
		}
	}
	return UnknownSpringBootVersion
}

func isSpringBootApplication(pom pom) bool {
	for _, dep := range pom.Dependencies {
		if dep.GroupId == "org.springframework.boot" {
			return true
		}
	}
	for _, dep := range pom.Build.Plugins {
		if dep.GroupId == "org.springframework.boot" {
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
