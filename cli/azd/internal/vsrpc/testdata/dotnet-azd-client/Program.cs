// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

using System.Net.Security;
using System.Net.WebSockets;
using System.Security.Cryptography.X509Certificates;
using StreamJsonRpc;

byte[] certBytes = Convert.FromBase64String(Settings.ServerCertBase64);
X509Certificate2 cert = new X509Certificate2(certBytes);

ClientWebSocket wsClientES = new ClientWebSocket();
wsClientES.Options.RemoteCertificateValidationCallback = ValidateCertificateCallback;
await wsClientES.ConnectAsync(new Uri("wss://127.0.0.1:8080/EnvironmentService/v1.0"), CancellationToken.None);

ClientWebSocket wsClientAS = new ClientWebSocket();
wsClientAS.Options.RemoteCertificateValidationCallback = ValidateCertificateCallback;
await wsClientAS.ConnectAsync(new Uri("wss://127.0.0.1:8080/AspireService/v1.0"), CancellationToken.None);

ClientWebSocket wsClientSS = new ClientWebSocket();
wsClientSS.Options.RemoteCertificateValidationCallback = ValidateCertificateCallback;
await wsClientSS.ConnectAsync(new Uri("wss://127.0.0.1:8080/ServerService/v1.0"), CancellationToken.None);

ClientWebSocket wsClientDS = new ClientWebSocket();
wsClientDS.Options.RemoteCertificateValidationCallback = ValidateCertificateCallback;
await wsClientDS.ConnectAsync(new Uri("wss://127.0.0.1:8080/TestDebugService/v1.0"), CancellationToken.None);

IEnvironmentService esSvc = JsonRpc.Attach<IEnvironmentService>(new WebSocketMessageHandler(wsClientES));
IAspireService asSvc = JsonRpc.Attach<IAspireService>(new WebSocketMessageHandler(wsClientAS));
IServerService ssSvc = JsonRpc.Attach<IServerService>(new WebSocketMessageHandler(wsClientSS));
IDebugService dsSvc = JsonRpc.Attach<IDebugService>(new WebSocketMessageHandler(wsClientDS));

{
    Console.WriteLine("== Testing Cancel ==");
    var cts = new CancellationTokenSource();
    var t = dsSvc.TestCancelAsync(1000 * 10, cts.Token);
    await Task.Delay(1000);
    Console.WriteLine("Cancelling");
    cts.Cancel();
    Console.WriteLine("Observing Task");
    try {
        var result = await t;
        Console.WriteLine($"TestCancelAsync completed with result: {result}");
    } catch (TaskCanceledException) {
        Console.WriteLine($"TestCancelAsync was cancelled");
    } catch (Exception e) {
        Console.WriteLine($"TestCancelAsync threw unexpected exception: {e}");
    }
}

{
    Console.WriteLine("== Testing IObservable ==");
    var intObserver = new WriterObserver<int>();
    var t = dsSvc.TestIObserverAsync(10, intObserver, CancellationToken.None);
    await Task.Delay(2000);
    await t;
    Console.WriteLine("== Done Testing IObservable ==");
}

bool ValidateCertificateCallback(object sender, X509Certificate? certificate, X509Chain? chain, SslPolicyErrors sslPolicyErrors) {
    return certificate != null && certificate.Equals(cert);
}

await RunLifecycle();

/*
 * Run an end to end life cycle test for AZD, we do the following steps:
 * 1. Initialize a new session.
 * 2. Call GetAspireHostAsync to fetch information about the host and print it.
 * 3. Call GetEnvironmentsAsync to fetch information about each environment and print it.
 * 4. If the Environment set in the EnvironmentName variable does not exist, create it.
 * 5. Call GetEnvironmentsAsync to fetch information about each environment and print it.
 * 6. Call OpenEnvironmentAsync to fetch brief information about the specific environment and print it.
 * 7. Call LoadEnvironmentAsync to fetch more detailed information about the specific environment and print it.
 * 8. Call RefreshEnvironmentAsync to fetch even more detailed information about the specific environment and print it.
 * 9. Call DeployAsync to deploy the specific environment, writing the output to stdout and then printing the result.
 * 10. Call SetCurrentEnvironmentAsync to set the specific environment as the current environment.
 */
#pragma warning disable CS8321 // The local function 'RunLifecycle' is declared but never used
async Task RunLifecycle() {
#pragma warning restore CS8321 // The local function 'RunLifecycle' is declared but never used
    if (string.IsNullOrEmpty(Settings.RootPath)) {
        throw new ArgumentException("RootPath must be set");
    }

    if (string.IsNullOrEmpty(Settings.EnvironmentName)) {
        throw new ArgumentException("EnvironmentName must be set");
    }

    if (string.IsNullOrEmpty(Settings.SubscriptionId)) {
        throw new ArgumentException("SubscriptionId must be set");
    }

    if (string.IsNullOrEmpty(Settings.Location)) {
        throw new ArgumentException("Location must be set");
    }

    bool hasEnvironment = false;

    IObserver<ProgressMessage> observer = new WriterObserver<ProgressMessage>();

    Console.WriteLine($"== Initializing ==");
    var session = await ssSvc.InitializeAsync(Settings.RootPath, CancellationToken.None);
    Console.WriteLine($"== Done Initializing ==");

    {
        Console.WriteLine("== Getting Host Info: Production ==");
        var result = await asSvc.GetAspireHostAsync(session, "Production", observer, CancellationToken.None);   
        Console.WriteLine($"Aspire Host: {result.Name} ({result.Path})");
        foreach (Service s in result.Services) {
            Console.WriteLine($"  Service: {s.Name} ({s.IsExternal})");
        }
        Console.WriteLine("== Got Host Info ==");
    }

    { 
        Console.WriteLine("== Getting Environments ==");
        foreach (var e in await esSvc.GetEnvironmentsAsync(session, observer, CancellationToken.None)) {
            Console.WriteLine($"Environment: {e.Name} IsCurrent: {e.IsCurrent}");           
            hasEnvironment = hasEnvironment || e.Name == Settings.EnvironmentName;
        }
        Console.WriteLine("== Got Environments ==");
    }

    if (!hasEnvironment)
    {
        Console.WriteLine($"== Creating Environment: {Settings.EnvironmentName} ==");
        Environment e = new Environment(Settings.EnvironmentName) {
            Properties = new Dictionary<string, string>() {
                { "ASPIRE_ENVIRONMENT", "Production" },
                { "Subscription", Settings.SubscriptionId },
                { "Location", Settings.Location}
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

        var result = await esSvc.CreateEnvironmentAsync(session, e, observer, CancellationToken.None);
        Console.WriteLine($"Created environment: {result}");
        Console.WriteLine("== Done Creating Environment ==");
    }

    { 
        Console.WriteLine("== Getting Environments ==");
        foreach (var e in await esSvc.GetEnvironmentsAsync(session, observer, CancellationToken.None)) {
            Console.WriteLine($"Environment: {e.Name} IsCurrent: {e.IsCurrent}");
        }
        Console.WriteLine("== Done Getting Environments ==");
    }

    { 
        Console.WriteLine($"== Opening Environment: {Settings.EnvironmentName} ==");
        var result = await esSvc.OpenEnvironmentAsync(session, Settings.EnvironmentName, observer, CancellationToken.None);
        WriteEnvironment(result);
        Console.WriteLine($"== Done Environment: {Settings.EnvironmentName} ==");
    }


    { 
        Console.WriteLine($"== Loading Environment: {Settings.EnvironmentName} ==");
        var result = await esSvc.LoadEnvironmentAsync(session, Settings.EnvironmentName, observer, CancellationToken.None);
        WriteEnvironment(result);
        Console.WriteLine($"== Done Loading Environment: {Settings.EnvironmentName} ==");
    }

    { 
        Console.WriteLine($"== Refreshing Environment: {Settings.EnvironmentName} ==");
        var result = await esSvc.RefreshEnvironmentAsync(session, Settings.EnvironmentName, observer, CancellationToken.None);
        WriteEnvironment(result);
        Console.WriteLine($"== Done Refreshing Environment: {Settings.EnvironmentName} ==");
    }

    {
        Console.WriteLine($"== Deploying Environment: {Settings.EnvironmentName} ==");
        var result = await esSvc.DeployAsync(session, Settings.EnvironmentName, observer, CancellationToken.None);
        WriteEnvironment(result);
        Console.WriteLine("== Done Deploying Environment ==");
    }

    {
        Console.WriteLine($"== Setting Current Environment: {Settings.EnvironmentName} ==");
        var result = await esSvc.SetCurrentEnvironmentAsync(session, Settings.EnvironmentName, observer, CancellationToken.None);
        Console.WriteLine($"Result: {result}");
        Console.WriteLine("== Done Setting Current Environment ==");
    }
}


#pragma warning disable CS8321 // The local function 'WriteEnvironment' is declared but never used
void WriteEnvironment(Environment e) {
#pragma warning restore CS8321 // The local function 'WriteEnvironment' is declared but never used
    Console.WriteLine($"Environment: {e.Name} {e.IsCurrent}");
    foreach (Service s in e.Services) {
         Console.WriteLine($"  Service: {s.Name}");
         Console.WriteLine($"    External: {s.IsExternal}");
         Console.WriteLine($"    Endpoint: {s.Endpoint}");
         Console.WriteLine($"    Resource: {s.ResourceId}");
    }
    foreach (KeyValuePair<string, string> kvp in e.Properties) {
         Console.WriteLine($"  Property: {kvp.Key} = {kvp.Value}");
    }
}

class WriterObserver<ProgressMessage> : IObserver<ProgressMessage>
{
    public void OnCompleted() => Console.WriteLine("Completed");
    public void OnError(Exception error) => Console.WriteLine($"Error: {error}");
    public void OnNext(ProgressMessage value) {
        var msg = value!.ToString()!;
        if (msg[msg.Length-1] == '\n') {
            Console.Write(msg);
        } else {
            Console.WriteLine(msg);
        }
    }
}
