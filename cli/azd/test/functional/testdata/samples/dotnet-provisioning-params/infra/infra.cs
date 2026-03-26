#:package Azure.Provisioning@1.6.0-alpha.20260325.1
#:package Azure.Provisioning.Storage@1.1.2

using Azure.Provisioning;
using Azure.Provisioning.Storage;

// args[0] = output directory (always provided by azd)
// args[1..n] = extra args forwarded from `azd provision -- <args>`
var outputDir = args.Length > 0 ? args[0] : "./generated";
Directory.CreateDirectory(outputDir);

// Parse extra arguments: --region <value> and --prefix <value>
var region = "eastus2";
var prefix = "test";

for (int i = 1; i < args.Length; i++)
{
    switch (args[i])
    {
        case "--region" when i + 1 < args.Length:
            region = args[++i];
            break;
        case "--prefix" when i + 1 < args.Length:
            prefix = args[++i];
            break;
    }
}

Console.WriteLine($"Using region: {region}, prefix: {prefix}");

var infra = new Infrastructure("main");

// Use the region parameter as the default location
var locationParam = new ProvisioningParameter("location", typeof(string))
{
    Value = new Azure.Provisioning.Expressions.StringLiteralExpression(region),
};
infra.Add(locationParam);

var storageAccount = new StorageAccount(prefix + "storage")
{
    Kind = StorageKind.StorageV2,
    Sku = new StorageSku { Name = StorageSkuName.StandardLrs },
    AllowSharedKeyAccess = false,
    MinimumTlsVersion = StorageMinimumTlsVersion.Tls1_2,
};
infra.Add(storageAccount);

var output = new ProvisioningOutput("storageAccountName", typeof(string))
{
    Value = storageAccount.Name,
};
infra.Add(output);

infra.Build().Save(outputDir);
Console.WriteLine($"Generated Bicep files to: {outputDir}");
