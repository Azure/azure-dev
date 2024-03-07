using Microsoft.Playwright;
using Should;
using System.Text.RegularExpressions;

namespace AspireAzdTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class RedisTests : TestBase
{

    [Test]
    public async Task RedisPubSubWorks()
    {
        await GetHomePage();

        // Click the get started link.
        await Page.GetByRole(AriaRole.Link, new() { Name = "Redis" })
            .ClickAsync();

        // Expects the URL to contain intro.
        await Expect(Page).ToHaveURLAsync(new Regex(".*redis"));

        await Task.Delay(2000);

        // expect the page to have a button
        var button = Page.GetByRole(AriaRole.Button, new() { Name = "Send" });

        // click the button to send the message
        await button.ClickAsync(new()
        {
            Timeout = 3000,
            Force = true
        });

        // make sure we have data
        (await Page.EvalOnSelectorAsync<int>("//table", "tbl => tbl.rows.length"))
            .ShouldBeGreaterThan(0);
    }
}
