const { Command } = require('commander');
const AzdClient = require('../azdClient');
const { AzureDeveloperCliCredential } = require('@azure/identity');
const { ResourceManagementClient } = require('@azure/arm-resources');
const logger = require('../logger');

const {
  MultiSelectRequest,
  MultiSelectOptions,
  MultiSelectChoice,
  ConfirmRequest,
  ConfirmOptions,
  SelectRequest,
  SelectOptions,
  SelectChoice,
  PromptSubscriptionRequest,
  PromptResourceGroupRequest,
  PromptResourceGroupResourceRequest,
  PromptSubscriptionResourceRequest,
  PromptResourceSelectOptions,
  PromptResourceOptions,
} = require('../generated/proto/prompt_pb');

const { 
  AzureContext,
  AzureScope,
} = require('../generated/proto/models_pb');

function createPromptCommand() {
  const cmd = new Command('prompt');
  cmd.description('Examples of prompting the user for input.');

  cmd.action(async () => {
    const client = new AzdClient();

    // === Multi-select
    logger.info('Prompting user for multi-select...');
    const multiSelectReq = new MultiSelectRequest();
    const multiOptions = new MultiSelectOptions();
    multiOptions.setMessage('Which Azure services do you use most with azd?');

    const multiChoices = [
      ['Container Apps', 'container-apps'],
      ['Functions', 'functions'],
      ['Static Web Apps', 'static-web-apps'],
      ['App Service', 'app-service'],
      ['Cosmos DB', 'cosmos-db'],
      ['SQL Database', 'sql-db'],
      ['Storage', 'storage'],
      ['Key Vault', 'key-vault'],
      ['Kubernetes Service', 'kubernetes-service']
    ].map(([label, value]) => {
      const c = new MultiSelectChoice();
      c.setLabel(label);
      c.setValue(value);
      return c;
    });

    multiOptions.setChoicesList(multiChoices);
    multiSelectReq.setOptions(multiOptions);

    const multiRes = await callPrompt(client.Prompt.multiSelect.bind(client.Prompt), multiSelectReq, client._metadata);
    logger.info('Selected services', { values: multiRes.getValuesList() });

    // === Confirm continue
    const confirmReq = new ConfirmRequest();
    const confirmOptions = new ConfirmOptions();
    confirmOptions.setMessage('Do you want to search for Azure resources?');
    confirmOptions.setDefaultValue(true);
    confirmReq.setOptions(confirmOptions);

    const confirmRes = await callPrompt(client.Prompt.confirm.bind(client.Prompt), confirmReq, client._metadata);
    if (!confirmRes.getValue()) {
      logger.info('Cancelled by user.');
      return;
    }

    // === Prompt for subscription
    const subRes = await callPrompt(
      client.Prompt.promptSubscription.bind(client.Prompt),
      new PromptSubscriptionRequest(),
      client._metadata
    );

    const subscription_id = subRes.getSubscription().getId();
    const tenant_id = subRes.getSubscription().getTenantId();
    logger.info('Subscription selected', { subscription_id });
    logger.info('Tenant', { tenant_id });

    const credential = new AzureDeveloperCliCredential({ tenantId: tenant_id });
    const armClient = new ResourceManagementClient(credential, subscription_id);

    let resource_type = '';

    // === Confirm filter by resource type
    const confirmTypeReq = new ConfirmRequest();
    const typeConfirmOptions = new ConfirmOptions();
    typeConfirmOptions.setMessage('Do you want to filter by resource type?');
    typeConfirmOptions.setDefaultValue(false);
    confirmTypeReq.setOptions(typeConfirmOptions);

    const filterType = await callPrompt(client.Prompt.confirm.bind(client.Prompt), confirmTypeReq, client._metadata);

    if (filterType.getValue()) {
      const providers = [];
      for await (const p of armClient.providers.list()) {
        if (p.registrationState === 'Registered') {
          providers.push(p);
        }
      }

      const selectProviderReq = new SelectRequest();
      const selectProviderOptions = new SelectOptions();
      selectProviderOptions.setMessage('Select a resource provider');
      const providerChoices = providers.map((p, i) => {
        const c = new SelectChoice();
        c.setLabel(p.namespace);
        c.setValue(i.toString());
        return c;
      });
      selectProviderOptions.setChoicesList(providerChoices);
      selectProviderReq.setOptions(selectProviderOptions);

      const providerRes = await callPrompt(client.Prompt.select.bind(client.Prompt), selectProviderReq, client._metadata);
      const selectedProvider = providers[parseInt(providerRes.getValue(), 10)];
      const resourceTypes = selectedProvider.resourceTypes || [];

      const selectTypeReq = new SelectRequest();
      const selectTypeOptions = new SelectOptions();
      selectTypeOptions.setMessage(`Select a ${selectedProvider.namespace} resource type`);
      const typeChoices = resourceTypes.map((rt, i) => {
        const c = new SelectChoice();
        c.setLabel(rt.resourceType);
        c.setValue(i.toString());
        return c;
      });
      selectTypeOptions.setChoicesList(typeChoices);
      selectTypeReq.setOptions(selectTypeOptions);

      const typeRes = await callPrompt(client.Prompt.select.bind(client.Prompt), selectTypeReq, client._metadata);
      const selectedType = resourceTypes[parseInt(typeRes.getValue(), 10)].resourceType;
      resource_type = `${selectedProvider.namespace}/${selectedType}`;
    }

    // === Confirm filter by resource group
    const confirmGroupReq = new ConfirmRequest();
    const groupOptions = new ConfirmOptions();
    groupOptions.setMessage('Do you want to filter by resource group?');
    groupOptions.setDefaultValue(false);
    confirmGroupReq.setOptions(groupOptions);

    const filterGroup = await callPrompt(client.Prompt.confirm.bind(client.Prompt), confirmGroupReq, client._metadata);

    const scope = new AzureScope();
    scope.setSubscriptionId(subscription_id);
    scope.setTenantId(tenant_id);
    
    const context = new AzureContext();
    context.setScope(scope);

    if (filterGroup.getValue()) {
      const rgReq = new PromptResourceGroupRequest();
      rgReq.setAzureContext(context);

      const rgRes = await callPrompt(client.Prompt.promptResourceGroup.bind(client.Prompt), rgReq, client._metadata);
      scope.setResourceGroup(rgRes.getResourceGroup().getName());
      context.setScope(scope);

      const rgrReq = new PromptResourceGroupResourceRequest();
      rgrReq.setAzureContext(context);

      const resOptions = new PromptResourceOptions();
      resOptions.setResourceType(resource_type);
      const selOpts = new PromptResourceSelectOptions();
      selOpts.setAllowNewResource(false);
      resOptions.setSelectOptions(selOpts);
      rgrReq.setOptions(resOptions);

      const resourceRes = await callPrompt(client.Prompt.promptResourceGroupResource.bind(client.Prompt), rgrReq, client._metadata);
      logSelected(resourceRes.getResource());
    } else {
      const subResReq = new PromptSubscriptionResourceRequest();
      subResReq.setAzureContext(context);

      const resOptions = new PromptResourceOptions();
      resOptions.setResourceType(resource_type);
      const selOpts = new PromptResourceSelectOptions();
      selOpts.setAllowNewResource(false);
      resOptions.setSelectOptions(selOpts);
      subResReq.setOptions(resOptions);

      const resourceRes = await callPrompt(client.Prompt.promptSubscriptionResource.bind(client.Prompt), subResReq, client._metadata);
      logSelected(resourceRes.getResource());
    }
  });

  return cmd;
}

function logSelected(resource) {
  if (!resource) {
    logger.info('No resource selected.');
    return;
  }

  logger.info('Selected Resource', {
    name: resource.getName(),
    type: resource.getType(),
    location: resource.getLocation(),
    id: resource.getId()
  });
}

function callPrompt(fn, req, metadata) {
  return new Promise((resolve, reject) => {
    fn(req, metadata, (err, res) => {
      if (err) return reject(err);
      resolve(res);
    });
  });
}

module.exports = { createPromptCommand };
