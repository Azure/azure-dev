// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

public class EnvironmentInfo
{
    public EnvironmentInfo(string name, string dotenvPath, bool isCurrent = false)
    {
        Name = name;
        IsCurrent = isCurrent;
        DotEnvPath = dotenvPath;
    }

    public string Name { get; }

    public bool IsCurrent { get; }

    public string DotEnvPath { get; }
}

public class Environment {
    public string Name { get; set; } = "";
    public bool IsCurrent { get; set; } = false;

    public Dictionary<string, string> Properties { get; set; } = new Dictionary<string, string>();

    public Service[] Services { get; set; } = [];

    public Dictionary<string, string> Values { get; set; } = new Dictionary<string, string>();
    public Resource[] Resources { get; set; } = [];
    public DeploymentResult? LastDeployment { get; set; }

    public Environment(string name) {
        Name = name;
    }
}

public class DeploymentResult {
    public string Success { get; set; } = "";
	public string Message { get; set; } = "";
	public string Time { get; set; } = "";
	public string DeploymentId { get; set; } = "";
}

public class Resource {
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string Type { get; set; } = "";
}

public class AspireHost {
    public string Name { get; set; } = "";
    public string Path { get; set; } = "";

    public Service[] Services { get; set; } = [];
}

public class Service {
    public string Name { get; set; }  = "";
	public bool IsExternal { get; set; }

    public string Path { get; set;}
    public string? Endpoint { get; set;}
    public string? ResourceId { get; set;}
}

public class Session {
    public string Id { get; set; } = "";
}

public class InitializeServerOptions {
    public string AuthenticationEndpoint { get; set; } = null;
    public string AuthenticationKey { get; set; } = null;
}

[Flags]
public enum EnvironmentDeleteMode
{
    None = 0,
    Local = 1,
    Remote = 2,
    All = Local | Remote
}

public class ProgressMessage
{
    public ProgressMessage(
        string message, MessageSeverity severity, DateTime time, MessageKind kind, string code, string additionalInfoLink)
    {
        Message = message;
        Severity = severity;
        Time = time;
        Kind = kind;
        Code = code;
        AdditionalInfoLink = additionalInfoLink;
    }

    public string Message;
    public MessageSeverity Severity;
    public DateTime Time;
    public MessageKind Kind;
    public string Code;
    public string AdditionalInfoLink;

    public override string ToString() => $"{Time}: {Kind} : {Severity} {Message}";
}

public class Context
{
    public Session Session;
    public string HostProjectPath;
}

public enum MessageSeverity
{
    Info = 0,
    Warning = 1,
    Error = 2,
}

public enum MessageKind
{
    Logging = 0,
    Important = 1
}

// To expose this, ensure that AZD_DEBUG_SERVER_DEBUG_ENDPOINTS is set to true when running `azd vs-server`.
public interface IDebugService {
    ValueTask<bool> TestCancelAsync(int timeoutMs, CancellationToken cancellationToken);
    ValueTask TestIObserverAsync(int max, IObserver<int> observer, CancellationToken cancellationToken);
}

public interface IServerService {
    ValueTask<Session> InitializeAsync(string rootPath, InitializeServerOptions options, CancellationToken cancellationToken);
    ValueTask StopAsync(CancellationToken cancellationToken);
}

public interface IEnvironmentService {
    ValueTask<IEnumerable<EnvironmentInfo>> GetEnvironmentsAsync(Context c,IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> OpenEnvironmentAsync(Context c,string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> LoadEnvironmentAsync(Context c,string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> RefreshEnvironmentAsync(Context c,string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<bool> DeleteEnvironmentAsync(Context c,string envName, EnvironmentDeleteMode mode, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<bool> CreateEnvironmentAsync(Context c,Environment newEnv,IObserver<ProgressMessage> outputObserver,  CancellationToken cancellationToken);
    ValueTask<bool> SetCurrentEnvironmentAsync(Context c,string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> DeployAsync(Context c,string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
}

public interface IAspireService {
    ValueTask<AspireHost> GetAspireHostAsync(Context c,string aspireEnvironment, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
}