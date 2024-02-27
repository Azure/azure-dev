
using Azure.Storage.Blobs;
using System.Reflection;

namespace AspireAzdTests.Web;

public class BlobUploader : IHostedService
{
    BlobServiceClient _blobServiceClient;

    public BlobUploader(BlobServiceClient blobServiceClient)
    {
        _blobServiceClient = blobServiceClient;
    }

    public async Task StartAsync(CancellationToken cancellationToken)
    {
        await Task.Delay(5000);

        var resources = Assembly.GetExecutingAssembly().GetManifestResourceNames();
        var markdowns = resources.Where(r => r.EndsWith(".md")).ToList();

        await _blobServiceClient.GetBlobContainerClient("markdown").CreateIfNotExistsAsync();

        foreach (var markdown in markdowns)
        {
            var blob = markdown.Replace("AspireAzdTests.Web.Resources.", "");
            var blobClient = _blobServiceClient.GetBlobContainerClient("markdown").GetBlobClient(blob);
            using var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(markdown);
            blobClient.Upload(stream);
        }
    }

    public Task StopAsync(CancellationToken cancellationToken) => Task.CompletedTask;
}
