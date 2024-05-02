// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

using Microsoft.Extensions.Configuration;
using System.Net.WebSockets;
using System.Security.Cryptography.X509Certificates;
using StreamJsonRpc;
using System.Security.Cryptography.X509Certificates;

namespace AzdVsServerTests;

[Parallelizable(ParallelScope.Self)]
[TestFixture]
public class TestBase
{
    protected string _port = string.Empty;
    protected string _subscriptionId = string.Empty;

    protected string _rootDir = string.Empty;

    protected string _location = string.Empty;

    protected string _certBytes = string.Empty;

    protected string[] _projects;

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
        _projects = config["APP_HOST_PATHS"].Split(",");
        _certBytes = config["CERTIFICATE_BYTES"] ?? throw new InvalidOperationException("CERTIFICATE_BYTES is not set");

        var host = $"wss://127.0.0.1:{_port}";
        var expectedCert = new X509Certificate2(Convert.FromBase64String(_certBytes));

        ClientWebSocket wsClientES = new ClientWebSocket();
        wsClientES.Options.RemoteCertificateValidationCallback = (sender, certificate, chain, sslPolicyErrors) => {
            return certificate != null && certificate.Equals(expectedCert!);
        };
        await wsClientES.ConnectAsync(new Uri($"{host}/EnvironmentService/v1.0"), CancellationToken.None);

        ClientWebSocket wsClientAS = new ClientWebSocket();
        wsClientAS.Options.RemoteCertificateValidationCallback = (sender, certificate, chain, sslPolicyErrors) => {
            return certificate != null && certificate.Equals(expectedCert!);
        };
        await wsClientAS.ConnectAsync(new Uri($"{host}/AspireService/v1.0"), CancellationToken.None);

        ClientWebSocket wsClientSS = new ClientWebSocket();
        wsClientSS.Options.RemoteCertificateValidationCallback = (sender, certificate, chain, sslPolicyErrors) => {
            return certificate != null && certificate.Equals(expectedCert!);
        };
        await wsClientSS.ConnectAsync(new Uri($"{host}/ServerService/v1.0"), CancellationToken.None);

        ClientWebSocket wsClientDS = new ClientWebSocket();
        wsClientDS.Options.RemoteCertificateValidationCallback = (sender, certificate, chain, sslPolicyErrors) => {
            return certificate != null && certificate.Equals(expectedCert!);
        };
        await wsClientDS.ConnectAsync(new Uri($"{host}/TestDebugService/v1.0"), CancellationToken.None);

        esSvc = JsonRpc.Attach<IEnvironmentService>(new WebSocketMessageHandler(wsClientES));
        asSvc = JsonRpc.Attach<IAspireService>(new WebSocketMessageHandler(wsClientAS));
        svrSvc = JsonRpc.Attach<IServerService>(new WebSocketMessageHandler(wsClientSS));
        dsSvc = JsonRpc.Attach<IDebugService>(new WebSocketMessageHandler(wsClientDS));
    }
}
