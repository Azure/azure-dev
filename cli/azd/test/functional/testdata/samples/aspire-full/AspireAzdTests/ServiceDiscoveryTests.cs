using Microsoft.Playwright;
using Should;
using System.Text.RegularExpressions;

namespace AspireAzdTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class ServiceDiscoveryTests : TestBase
{

    [Test]
    public async Task ServiceToServiceWorks()
    {
        await GetHomePage();

        // Click the get started link.
        await Page.GetByRole(AriaRole.Link, new() { Name = "Weather" })
            .ClickAsync();

        // Expects the URL to contain intro.
        await Expect(Page).ToHaveURLAsync(new Regex(".*weather"));

        // wait for the weather forecast to load
        await Task.Delay(2000);

        // make sure we have data
        (await Page.EvalOnSelectorAsync<int>("//table", "tbl => tbl.rows.length"))
            .ShouldBeGreaterThan(3);
    }
}
