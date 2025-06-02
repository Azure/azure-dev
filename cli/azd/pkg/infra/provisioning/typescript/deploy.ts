// Example deploy.ts for TypeScriptProvider
// import { DefaultAzureCredential } from '@azure/identity';
// import { ResourceManagementClient } from '@azure/arm-resources';

// const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID;
// const resourceGroup = process.env.AZURE_ENV_NAME;
// const location = process.env.AZURE_LOCATION;

// export async function deploy() {
//   const credential = new DefaultAzureCredential();
//   const client = new ResourceManagementClient(credential, subscriptionId);
//   await client.resourceGroups.createOrUpdate(resourceGroup, { location });
//   // ...provision more resources
//   return {
//     outputs: {
//       websiteUrl: { type: 'string', value: `https://${resourceGroup}.azurewebsites.net` },
//     },
//   };
// }

// if (require.main === module) {
//   deploy().then(result => {
//     console.log(JSON.stringify(result));
//   }).catch(err => {
//     console.error(err);
//     process.exit(1);
//   });
// }
