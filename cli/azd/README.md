# [Spring PetClinic Sample Application](https://github.com/spring-projects/spring-petclinic) using a full set of Azure solutions

* [Azure AppService](https://azure.microsoft.com/en-us/products/app-service/) for app hosting,
* [Azure Database for MySQL](https://azure.microsoft.com/en-us/products/mysql/) for storage (optional, default is H2 in-memory database),
* [Azure Monitor](https://azure.microsoft.com/en-us/products/monitor/)([Application Insights](https://learn.microsoft.com/en-us/azure/azure-monitor/app/app-insights-overview?tabs=net)) for monitoring and logging.
* [Managed Identity](https://learn.microsoft.com/en-us/entra/identity/managed-identities-azure-resources/overview) for passwordless secure connections. 
* [Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview)

## Prerequisites

The following prerequisites are required to use this application. Please ensure that you have them all installed locally.

* [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli)
* Java 11 or later

## Tutorial

A blog post tutorial for this application can be found [here](https://techcommunity.microsoft.com/t5/apps-on-azure-blog/deploy-intelligent-springboot-apps-using-azure-openai-and-azure/ba-p/4257130).

Clone the code using `azd init -t Azure-Samples/SpringBoot-Petclinic-AI-Chat-on-App-Service`. 

Deploy with AZD using `azd up`. By default, an in-memory database (H2) is used. To deploy and use an Azure MySQL Database, switch your Spring profile from h2 to mysql and uncomment the mysql database creation code in the bicep files under the `infra/` directory. 
