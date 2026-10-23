param location string
param serviceName string
param environmentId string
param registryEndpoint string
param port int
param profiles array
param appImage string
param engine string
param databaseName string = ''
param host string = ''
param username string
@secure()
param password string = ''
param authentication string
param environmentBindings bool
param jdbcOptions string = ''
param driver string = ''
param embeddedUrl string = ''
param embeddedUrlConfigured bool
param embeddedDriver string = ''

var prefix = engine == 'mysql' ? 'MYSQL' : 'POSTGRES'
var usesPassword = engine != 'none' && authentication == 'password'
var databaseEnv = engine != 'none'
  ? concat(
      [
        { name: '${prefix}_HOST', value: host }
        { name: '${prefix}_PORT', value: engine == 'mysql' ? '3306' : '5432' }
        { name: '${prefix}_DATABASE', value: databaseName }
        { name: '${prefix}_USERNAME', value: username }
      ],
      usesPassword ? [{ name: '${prefix}_PASSWORD', secretRef: 'database-password' }] : []
    )
  : []
var scheme = engine == 'mysql' ? 'mysql' : 'postgresql'
var databasePort = engine == 'mysql' ? '3306' : '5432'
var optionSuffix = empty(jdbcOptions) ? '' : '?${jdbcOptions}'
var externalSpringEnv = engine != 'none'
  ? concat(
      [
        {
          name: 'SPRING_DATASOURCE_URL'
          value: 'jdbc:${scheme}://${host}:${databasePort}/${databaseName}${optionSuffix}'
        }
        { name: 'SPRING_DATASOURCE_USERNAME', value: username }
      ],
      empty(driver) ? [] : [{ name: 'SPRING_DATASOURCE_DRIVER_CLASS_NAME', value: driver }],
      usesPassword
        ? [{ name: 'SPRING_DATASOURCE_PASSWORD', secretRef: 'database-password' }]
        : [
            { name: 'SPRING_DATASOURCE_PASSWORD', value: '' }
            { name: 'SPRING_SQL_INIT_MODE', value: 'never' }
            { name: 'SPRING_JPA_HIBERNATE_DDL_AUTO', value: 'none' }
          ]
    )
  : []
var embeddedSpringEnv = concat(
  embeddedUrlConfigured ? [{ name: 'SPRING_DATASOURCE_URL', value: embeddedUrl }] : [],
  empty(embeddedDriver) ? [] : [{ name: 'SPRING_DATASOURCE_DRIVER_CLASS_NAME', value: embeddedDriver }]
)
var deploymentSpringEnv = engine != 'none' ? externalSpringEnv : embeddedSpringEnv
var placeholder = appImage == 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'

resource app 'Microsoft.App/containerApps@2024-03-01' = {
  name: serviceName
  location: location
  tags: { 'azd-service-name': serviceName }
  identity: { type: 'SystemAssigned' }
  properties: {
    managedEnvironmentId: environmentId
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: { external: true, targetPort: placeholder ? 80 : port, transport: 'auto' }
      registries: [{ server: registryEndpoint, identity: 'system' }]
      secrets: usesPassword ? [{ name: 'database-password', value: password }] : []
    }
    template: {
      containers: [
        {
          name: 'main'
          image: appImage
          resources: { cpu: json('0.5'), memory: '1Gi' }
          env: concat(
            [
              { name: 'SPRING_PROFILES_ACTIVE', value: join(profiles, ',') }
              // The analyzed defaults are already included in the pinned active profiles.
              { name: 'SPRING_PROFILES_DEFAULT', value: 'none' }
              { name: 'SERVER_ADDRESS', value: '0.0.0.0' }
              { name: 'SERVER_PORT', value: string(port) }
            ],
            environmentBindings
              ? deploymentSpringEnv
              : concat(
                  [
                    { name: 'SPRING_CONFIG_ADDITIONAL_LOCATION', value: 'file:/app/.azd-spring/application.properties' }
                  ],
                  databaseEnv
                )
          )
          probes: placeholder
            ? []
            : [
                {
                  type: 'Startup'
                  tcpSocket: { port: port }
                  initialDelaySeconds: 1
                  periodSeconds: 1
                  timeoutSeconds: 3
                  failureThreshold: 240
                }
                {
                  type: 'Liveness'
                  tcpSocket: { port: port }
                  initialDelaySeconds: 10
                  periodSeconds: 10
                  timeoutSeconds: 1
                  failureThreshold: 3
                }
                {
                  type: 'Readiness'
                  tcpSocket: { port: port }
                  initialDelaySeconds: 10
                  periodSeconds: 5
                  failureThreshold: 20
                }
              ]
        }
      ]
      scale: { minReplicas: 1, maxReplicas: 1 }
    }
  }
}

output appId string = app.id
output endpoint string = 'https://${app.properties.configuration.ingress.fqdn}'
