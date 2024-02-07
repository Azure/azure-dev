// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

public class EnvironmentInfo
{
    public EnvironmentInfo(string name, bool isCurrent = false)
    {
        Name = name;
        IsCurrent = isCurrent;
    }

    public string Name { get; }

    public bool IsCurrent { get; }
}

public class Environment {
    public string Name { get; set; } = "";
    public bool IsCurrent { get; set; } = false;

    public Dictionary<string, string> Properties { get; set; } = new Dictionary<string, string>();

    public Service[] Services { get; set; } = [];

    public Environment(string name) {
        Name = name;
    }
}

public class AspireHost {
    public string Name { get; set; } = "";
    public string Path { get; set; } = "";

    public Service[] Services { get; set; } = [];

    public string? Kind { get; set; }
    public string? Endpoint { get; set; }
    public string? ResourceId { get; set; }
}

public class Service {
    public string Name { get; set; }  = "";
	public bool IsExternal { get; set; }

    public string? Kind { get; set;}
    public string? Endpoint { get; set;}
    public string? ResourceId { get; set;}
}

public class Session {
    public string Id { get; set; } = "";
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

    public override string ToString() => $"{Time}: {Severity} {Message}";
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
    ValueTask<Session> InitializeAsync(string rootPath, CancellationToken cancellationToken);
}

public interface IEnvironmentService {
    ValueTask<IEnumerable<EnvironmentInfo>> GetEnvironmentsAsync(Session s, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> OpenEnvironmentAsync(Session s, string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> LoadEnvironmentAsync(Session s, string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> RefreshEnvironmentAsync(Session s, string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<bool> CreateEnvironmentAsync(Session s, Environment newEnv,IObserver<ProgressMessage> outputObserver,  CancellationToken cancellationToken);
    ValueTask<bool> SetCurrentEnvironmentAsync(Session s, string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
    ValueTask<Environment> DeployAsync(Session s, string envName, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
}

public interface IAspireService {
    ValueTask<AspireHost> GetAspireHostAsync(Session s, string aspireEnvironment, IObserver<ProgressMessage> outputObserver, CancellationToken cancellationToken);
}