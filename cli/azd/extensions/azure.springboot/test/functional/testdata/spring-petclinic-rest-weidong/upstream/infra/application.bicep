param location string
param serviceName string = 'spring-petclinic-rest-app'
param environmentId string
param registryEndpoint string
param databaseHost string
param appImage string

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
      // The bootstrap image must never become the public API.
      ingress: placeholder
        ? null
        : {
            external: true
            allowInsecure: false
            targetPort: 9966
            transport: 'http'
          }
      registries: [{ server: registryEndpoint, identity: 'system' }]
    }
    template: {
      containers: [
        {
          name: 'app'
          image: appImage
          resources: { cpu: json('0.5'), memory: '1Gi' }
          env: [
            { name: 'POSTGRES_URL', value: 'jdbc:postgresql://${databaseHost}:5432/petclinic' }
          ]
          probes: placeholder
            ? []
            : [
                {
                  type: 'Startup'
                  tcpSocket: { port: 9966 }
                  periodSeconds: 5
                  failureThreshold: 60
                }
                {
                  type: 'Readiness'
                  tcpSocket: { port: 9966 }
                  periodSeconds: 10
                  failureThreshold: 3
                }
              ]
        }
      ]
      scale: { minReplicas: 1, maxReplicas: 1 }
    }
  }
}

output SERVICE_SPRING_PETCLINIC_REST_APP_ENDPOINT_URL string = placeholder
  ? ''
  : 'https://${app.properties.configuration.ingress.fqdn}/petclinic'
