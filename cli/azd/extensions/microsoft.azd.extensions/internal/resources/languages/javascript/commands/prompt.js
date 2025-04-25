const { Command } = require('commander');
const AzdClient = require('../azdClient');
const { DefaultAzureCredential } = require('@azure/identity');
const { ResourceManagementClient } = require('@azure/arm-resources');

function createPromptCommand() {
  const cmd = new Command('prompt');
  cmd.description('Examples of prompting the user for input.');

  cmd.action(async () => {
    const client = new AzdClient();

    console.log('[prompt] Prompting user for multi-select...');

    // === Prompt for multi-select
    await new Promise((resolve, reject) => {
      client.Prompt.multiSelect({
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
      }, client._metadata, (err, res) => {
        if (err) return reject(err);
        console.log('[prompt] Selected:', res.values);
        resolve();
      });
    });

    // === Confirm whether to continue
    const confirm = await new Promise((resolve, reject) => {
      client.Prompt.confirm({
        options: {
          message: 'Do you want to search for Azure resources?',
          default_value: true
        }
      }, client._metadata, (err, res) => {
        if (err) return reject(err);
        resolve(res);
      });
    });

    if (!confirm?.value) {
      console.log('[prompt] Cancelled by user.');
      return;
    }

    // === Prompt for subscription
    const subRes = await new Promise((resolve, reject) => {
      client.Prompt.promptSubscription({}, client._metadata, (err, res) => {
        if (err) return reject(err);
        resolve(res);
      });
    });

    const subscription_id = subRes.subscription.id;
    const tenant_id = subRes.subscription.tenant_id;
    console.log('[prompt] Subscription:', subscription_id);
    console.log('[prompt] Tenant:', tenant_id);

    const credential = new DefaultAzureCredential();
    const armClient = new ResourceManagementClient(credential, subscription_id);

    let resource_type = '';

    // === Ask to filter by resource type
    const filterType = await new Promise((resolve, reject) => {
      client.Prompt.confirm({
        options: {
          message: 'Do you want to filter by resource type?',
          default_value: false
        }
      }, client._metadata, (err, res) => {
        if (err) return reject(err);
        resolve(res);
      });
    });

    if (filterType?.value) {
      const providers = [];
      for await (const p of armClient.providers.list()) {
        if (p.registrationState === 'Registered') {
          providers.push(p);
        }
      }

      const selectProviderRes = await new Promise((resolve, reject) => {
        client.Prompt.select({
          options: {
            message: 'Select a resource provider',
            choices: providers.map((p, i) => ({
              label: p.namespace,
              value: i.toString()
            }))
          }
        }, client._metadata, (err, res) => {
          if (err) return reject(err);
          resolve(res);
        });
      });

      const selectedProvider = providers[selectProviderRes.value];
      const resourceTypes = selectedProvider.resourceTypes || [];

      const selectTypeRes = await new Promise((resolve, reject) => {
        client.Prompt.select({
          options: {
            message: `Select a ${selectedProvider.namespace} resource type`,
            choices: resourceTypes.map((rt, i) => ({
              label: rt.resourceType,
              value: i.toString()
            }))
          }
        }, client._metadata, (err, res) => {
          if (err) return reject(err);
          resolve(res);
        });
      });

      resource_type = `${selectedProvider.namespace}/${resourceTypes[selectTypeRes.value].resourceType}`;
    }

    // === Ask to filter by resource group
    const filterGroup = await new Promise((resolve, reject) => {
      client.Prompt.confirm({
        options: {
          message: 'Do you want to filter by resource group?',
          default_value: false
        }
      }, client._metadata, (err, res) => {
        if (err) return reject(err);
        resolve(res);
      });
    });

    let context = {
      scope: {
        subscription_id,
        tenant_id
      }
    };

    if (filterGroup?.value) {
      // === Prompt for resource group
      const rgRes = await new Promise((resolve, reject) => {
        client.Prompt.promptResourceGroup({
          azure_context: context
        }, client._metadata, (err, res) => {
          if (err) return reject(err);
          resolve(res);
        });
      });

      context.scope.resource_group = rgRes.resource_group.name;

      const resourceRes = await new Promise((resolve, reject) => {
        client.Prompt.promptResourceGroupResource({
          azure_context: context,
          options: {
            resource_type,
            select_options: {
              allow_new_resource: false
            }
          }
        }, client._metadata, (err, res) => {
          if (err) return reject(err);
          resolve(res);
        });
      });

      logSelected(resourceRes?.resource);
    } else {
      const resourceRes = await new Promise((resolve, reject) => {
        client.Prompt.promptSubscriptionResource({
          azure_context: context,
          options: {
            resource_type,
            select_options: {
              allow_new_resource: false
            }
          }
        }, client._metadata, (err, res) => {
          if (err) return reject(err);
          resolve(res);
        });
      });

      logSelected(resourceRes?.resource);
    }
  });

  return cmd;
}

function logSelected(resource) {
  if (!resource) {
    console.log('[prompt] No resource selected.');
    return;
  }

  console.log('[prompt] Selected Resource:');
  console.log('  Name:', resource.name);
  console.log('  Type:', resource.type);
  console.log('  Location:', resource.location);
  console.log('  ID:', resource.id);
}

module.exports = { createPromptCommand };
