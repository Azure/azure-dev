// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

using Should;
using System.Text.RegularExpressions;

namespace AzdVsServerTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class AcceptanceTests : TestBase
{
    [Test]
    public async Task ManageEnvironments()
    {
        IObserver<ProgressMessage> observer = new WriterObserver<ProgressMessage>();
        var session = await svrSvc.InitializeAsync(_rootDir, CancellationToken.None);
        var result = await asSvc.GetAspireHostAsync(session, "Production", observer, CancellationToken.None);       
        result.Services.Count.ShouldEqual(2);
        result.Services[0].Path.ShouldNotBeEmpty();
        result.Services[1].Path.ShouldNotBeEmpty();

        var environments = (await esSvc.GetEnvironmentsAsync(session, observer, CancellationToken.None)).ToList();
        environments.ShouldBeEmpty();

        Environment e = new Environment("env1") {
            Properties = new Dictionary<string, string>() {
                { "ASPIRE_ENVIRONMENT", "Production" },
                { "Subscription", _subscriptionId },
                { "Location", _location}
            },
            Services = [
                new Service() {
                    Name = "apiservice",
                    IsExternal = false,
                },
                new Service() {
                    Name = "webfrontend",
                    IsExternal = true,
                }
            ],
        };

        await esSvc.CreateEnvironmentAsync(session, e, observer, CancellationToken.None);

        environments = (await esSvc.GetEnvironmentsAsync(session, observer, CancellationToken.None)).ToList();
        environments.ShouldNotBeEmpty();
        environments.Count.ShouldEqual(1);
        environments[0].Name.ShouldEqual(e.Name);
        environments[0].IsCurrent.ShouldBeTrue();
        environments[0].DotEnvPath.ShouldNotBeEmpty();

        Environment e2 = new Environment("env2") {
            Properties = new Dictionary<string, string>() {
                { "ASPIRE_ENVIRONMENT", "Production" },
                { "Subscription", _subscriptionId },
                { "Location", _location}
            },
            Services = e.Services,
        };

        await esSvc.CreateEnvironmentAsync(session, e2, observer, CancellationToken.None);

        environments = (await esSvc.GetEnvironmentsAsync(session, observer, CancellationToken.None)).ToList();
        environments.ShouldNotBeEmpty();
        environments.Count.ShouldEqual(2);

        var openEnv = await esSvc.OpenEnvironmentAsync(session, e.Name, observer, CancellationToken.None);
        openEnv.Name.ShouldEqual(e.Name);
        openEnv.IsCurrent.ShouldBeFalse();
        openEnv.Services.Count.ShouldEqual(2);
        openEnv.Services[0].Path.ShouldNotBeEmpty();
        openEnv.Services[1].Path.ShouldNotBeEmpty();

        openEnv = await esSvc.OpenEnvironmentAsync(session, e2.Name, observer, CancellationToken.None);
        openEnv.Name.ShouldEqual(e2.Name);
        openEnv.IsCurrent.ShouldBeTrue();
        openEnv.Services.Count.ShouldEqual(2);
        openEnv.Services[0].Path.ShouldNotBeEmpty();
        openEnv.Services[1].Path.ShouldNotBeEmpty();

        await esSvc.SetCurrentEnvironmentAsync(session, e.Name, observer, CancellationToken.None);
        openEnv = await esSvc.OpenEnvironmentAsync(session, e.Name, observer, CancellationToken.None);
        openEnv.Name.ShouldEqual(e.Name);
        openEnv.IsCurrent.ShouldBeTrue();
    }

    [Test]
    public async Task Cancellation() {
        var cts = new CancellationTokenSource();
        var cancelOp = dsSvc.TestCancelAsync(1000 * 10, cts.Token);
        await Task.Delay(1000);
        cts.Cancel();

        try {
            var result = await cancelOp;
            Assert.Fail("TestCancelAsync should have been cancelled");
        } catch (TaskCanceledException) {
        }

        var recorder = new Recorder<int>();
        var observe = dsSvc.TestIObserverAsync(10, recorder, CancellationToken.None);
        await Task.Delay(2000);
        await observe;

        recorder.Values.Count.ShouldEqual(10);
    }
}
