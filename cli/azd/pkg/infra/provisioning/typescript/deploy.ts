// Example deploy.ts for TypeScriptProvider
// import { DefaultAzureCredential } from '@azure/identity';
// import { ResourceManagementClient } from '@azure/arm-resources';

// const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID;
// const resourceGroup = process.env.AZURE_ENV_NAME;
// const requestedLocation = process.env.AZURE_LOCATION;

// export async function deploy() {
//   const credential = new DefaultAzureCredential();
//   const client = new ResourceManagementClient(credential, subscriptionId);
//   
//   // Check if the resource group already exists
//   let actualLocation = requestedLocation;
//   try {
//     const existingGroup = await client.resourceGroups.get(resourceGroup);
//     if (existingGroup && existingGroup.location) {
//       // Use the existing resource group's location
//       // Use stderr for logs to avoid interfering with JSON output
//       console.error(`Resource group ${resourceGroup} already exists in location ${existingGroup.location}. Using existing location.`);
//       actualLocation = existingGroup.location;
//     }
//   } catch (error) {
//     // Resource group doesn't exist, will be created with requested location
//     console.error(`Creating new resource group ${resourceGroup} in ${requestedLocation}`);
//   }
//   
//   // Create or update the resource group with the correct location
//   await client.resourceGroups.createOrUpdate(resourceGroup, { location: actualLocation });
//   
//   // ...provision more resources
//   return {
//     outputs: {
//       websiteUrl: { type: 'string', value: `https://${resourceGroup}.azurewebsites.net` },
//       location: { type: 'string', value: actualLocation },
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
