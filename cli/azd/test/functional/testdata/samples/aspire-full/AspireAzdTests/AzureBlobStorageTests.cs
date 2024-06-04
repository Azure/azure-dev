using Microsoft.Playwright;
using Should;
using System.Text.RegularExpressions;

namespace AspireAzdTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public  class AzureBlobStorageTests : TestBase
{
    [Test]
    public async Task BlobListIsEvident()
    {
        await GetHomePage();

        // Click the get started link.
        await Page.GetByRole(AriaRole.Link, new() { Name = "Azure Blobs" })
            .ClickAsync();

        // Expects the URL to contain intro.
        await Expect(Page).ToHaveURLAsync(new Regex(".*azureblobs"));

        // wait for the data to load
        await Task.Delay(2000);

        // make sure we have data
        (await Page.EvalOnSelectorAsync<int>("//table", "tbl => tbl.rows.length"))
            .ShouldBeGreaterThan(1);
    }
}
