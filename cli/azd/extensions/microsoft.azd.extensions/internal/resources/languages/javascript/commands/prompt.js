const { Command } = require('commander');
const { AzdClient } = require('../azdClient');

function createPromptCommand() {
  const cmd = new Command('prompt');
  cmd.description('Examples of prompting the user for input.');

  cmd.action(async () => {
    const client = new AzdClient();

    // === Prompt for multi-select
    await client.Prompt.multiSelect({
      options: {
        message: 'Which Azure services do you use most with AZD?',
        choices: [
          { label: 'Container Apps', value: 'container-apps' },
          { label: 'Functions', value: 'functions' },
          { label: 'Static Web Apps', value: 'static-web-apps' },
          { label: 'App Service', value: 'app-service' },
          { label: 'Cosmos DB', value: 'cosmos-db' },
          { label: 'SQL Database', value: 'sql-db' },
          { label: 'Storage', value: 'storage' },
          { label: 'Key Vault', value: 'key-vault' },
          { label: 'Kubernetes Service', value: 'kubernetes-service' }
        ]
      }
    });

    // === Confirm
    const confirm = await client.Prompt.confirm({
      options: {
        message: 'Do you want to search for Azure resources?',
        defaultValue: true
      }
    });

    if (!confirm?.value) {
      console.log('Search cancelled.');
      return;
    }

    // === Prompt for subscription
    const sub = await client.Prompt.promptSubscription({});
    const subscriptionId = sub?.subscription?.id;
    const tenantId = sub?.subscription?.tenantId;

    console.log(`Subscription ID: ${subscriptionId}`);
    console.log(`Tenant ID: ${tenantId}`);

    // === Prompt for resource group
    const resourceGroup = await client.Prompt.promptResourceGroup({
      azureContext: { scope: { subscriptionId, tenantId } }
    });

    const resource = await client.Prompt.promptResourceGroupResource({
      azureContext: { scope: { subscriptionId, tenantId, resourceGroup: resourceGroup?.resourceGroup?.name } },
      options: {
        selectOptions: {
          allowNewResource: false
        }
      }
    });

    if (resource?.resource) {
      console.log('Selected Resource:');
      console.log('Name:', resource.resource.name);
      console.log('Type:', resource.resource.type);
      console.log('Location:', resource.resource.location);
      console.log('ID:', resource.resource.id);
    } else {
      console.log('No resource selected.');
    }
  });

  return cmd;
}

module.exports = { createPromptCommand };
