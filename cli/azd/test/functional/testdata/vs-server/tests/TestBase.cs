// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

using Microsoft.Extensions.Configuration;
using System.Net.WebSockets;
using StreamJsonRpc;

namespace AzdVsServerTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class TestBase
{
    protected string _port = string.Empty;
    protected string _subscriptionId = string.Empty;

    protected string _rootDir = string.Empty;

    protected string _location = string.Empty;

    // Environment name for live tests
    protected string _envName = string.Empty;


    protected IEnvironmentService esSvc;

    protected IAspireService asSvc;

    protected IServerService svrSvc;

    protected IDebugService dsSvc;

    [OneTimeSetUp]
    public async Task OneTimeSetup()
    {
        var builder = new ConfigurationBuilder()
            .AddEnvironmentVariables();
        var config = builder.Build();
        _port = config["PORT"] ?? "8080";
        _subscriptionId = config["AZURE_SUBSCRIPTION_ID"] ?? "any";
        _rootDir = config["ROOT_DIR"] ?? System.IO.Directory.GetCurrentDirectory();
        _location = config["AZURE_LOCATION"] ?? "westus2";
        _envName = config["AZURE_ENV_NAME"] ?? "vs-server-env";

        var host = $"ws://127.0.0.1:{_port}";

        ClientWebSocket wsClientES = new ClientWebSocket();
        await wsClientES.ConnectAsync(new Uri($"{host}/EnvironmentService/v1.0"), CancellationToken.None);

        ClientWebSocket wsClientAS = new ClientWebSocket();
        await wsClientAS.ConnectAsync(new Uri($"{host}/AspireService/v1.0"), CancellationToken.None);

        ClientWebSocket wsClientSS = new ClientWebSocket();
        await wsClientSS.ConnectAsync(new Uri($"{host}/ServerService/v1.0"), CancellationToken.None);

        ClientWebSocket wsClientDS = new ClientWebSocket();
        await wsClientDS.ConnectAsync(new Uri($"{host}/TestDebugService/v1.0"), CancellationToken.None);

        esSvc = JsonRpc.Attach<IEnvironmentService>(new WebSocketMessageHandler(wsClientES));
        asSvc = JsonRpc.Attach<IAspireService>(new WebSocketMessageHandler(wsClientAS));
        svrSvc = JsonRpc.Attach<IServerService>(new WebSocketMessageHandler(wsClientSS));
        dsSvc = JsonRpc.Attach<IDebugService>(new WebSocketMessageHandler(wsClientDS));
    }
}
