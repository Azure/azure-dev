using Microsoft.Extensions.Configuration;
using Microsoft.Playwright.NUnit;

namespace AspireAzdTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class TestBase : PageTest
{
    protected string _url = string.Empty;

    [OneTimeSetUp]
    public void OneTimeSetup()
    {
        var builder = new ConfigurationBuilder()
            .AddEnvironmentVariables()
            .AddUserSecrets(typeof(HomePageTests).Assembly);
        var config = builder.Build();

        _url = config["LIVE_APP_URL"] ?? string.Empty;
    }

    protected async Task GetHomePage()
    {
        if (!string.IsNullOrEmpty(_url))
        {
            await Page.GotoAsync(_url);
            await Expect(Page).ToHaveTitleAsync("Home");
        }
    }
}
