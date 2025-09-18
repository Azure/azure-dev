using Microsoft.Playwright;
using System.Text.RegularExpressions;

namespace AspireAzdTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class HomePageTests : TestBase
{
    [Test]
    public async Task AppIsDeployed()
    {
        await GetHomePage();
    }

    [Test]
    public async Task CounterWorks()
    {
        await GetHomePage();

        // create a locator
        var counterLink = Page.GetByRole(AriaRole.Link, new() { Name = "Counter" });

        // Expect an attribute "to be strictly equal" to the value.
        await Expect(counterLink).ToHaveAttributeAsync("href", "counter");

        // Click the get started link.
        await counterLink.ClickAsync();

        // Expects the URL to contain intro.
        await Expect(Page).ToHaveURLAsync(new Regex(".*counter"));

        // wait for the weather forecast to load
        await Task.Delay(2000);

        // locate the counter status label
        await Expect(Page.Locator("#status"))
            .ToHaveTextAsync("Current count: 0");

        // click the button to increment the counter
        await Page.Locator("#counterButton").ClickAsync(new()
        {
            Force = true,
            Timeout = 3000
        });

        // locate the counter status label and check to see if it's updated
        await Expect(Page.Locator("#status"))
            .ToHaveTextAsync("Current count: 1");
    }
}