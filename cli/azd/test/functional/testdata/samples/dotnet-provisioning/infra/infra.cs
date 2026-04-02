#:package Azure.Provisioning@1.6.0-alpha.20260325.1
#:package Azure.Provisioning.Storage@1.1.2

using Azure.Provisioning;
using Azure.Provisioning.Storage;

// The output directory is passed as the first command-line argument by azd.
var outputDir = args.Length > 0 ? args[0] : "./generated";
Directory.CreateDirectory(outputDir);

var infra = new Infrastructure("main");

// Create a simple storage account for E2E testing
var storageAccount = new StorageAccount("teststorage")
{
    Kind = StorageKind.StorageV2,
    Sku = new StorageSku { Name = StorageSkuName.StandardLrs },
    AllowSharedKeyAccess = false,
    MinimumTlsVersion = StorageMinimumTlsVersion.Tls1_2,
};
infra.Add(storageAccount);

// Add an output so azd can capture the storage account name
var output = new ProvisioningOutput("storageAccountName", typeof(string))
{
    Value = storageAccount.Name,
};
infra.Add(output);

// Compile C# to Bicep and save to the output directory
infra.Build().Save(outputDir);

Console.WriteLine($"Generated Bicep files to: {outputDir}");
