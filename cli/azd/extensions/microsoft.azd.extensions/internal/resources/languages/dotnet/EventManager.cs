using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Grpc.Core;
using Microsoft.Azd;

namespace Microsoft.Azd
{
    public class ProjectEventArgs
    {
        public ProjectConfig Project { get; set; } = default!;
    }

    public class ServiceEventArgs
    {
        public ProjectConfig Project { get; set; } = default!;
        public ServiceConfig Service { get; set; } = default!;
    }

    public class ServerEventOptions
    {
        public string? Host { get; set; }
        public string? Language { get; set; }
    }

    public class EventManager : IAsyncDisposable
    {
        private readonly AzdClient _azdClient;
        private AsyncDuplexStreamingCall<EventMessage, EventMessage>? _stream;

        private readonly Dictionary<string, Func<ProjectEventArgs, Task>> _projectHandlers = new();
        private readonly Dictionary<string, Func<ServiceEventArgs, Task>> _serviceHandlers = new();

        public EventManager(AzdClient azdClient)
        {
            _azdClient = azdClient;
        }

        public async ValueTask DisposeAsync()
        {
            if (_stream != null)
            {
                await _stream.RequestStream.CompleteAsync();
            }
        }

        private Task EnsureInitializedAsync(CancellationToken cancellationToken)
        {
            if (_stream == null)
            {
                _stream = _azdClient.Events.EventStream(cancellationToken: cancellationToken);
            }
            return Task.CompletedTask;
        }

        public async Task ReceiveAsync(CancellationToken cancellationToken)
        {
            await EnsureInitializedAsync(cancellationToken);
            await SendReadyEventAsync();

            try
            {
                while (!cancellationToken.IsCancellationRequested)
                {
                    if (_stream == null || !await _stream.ResponseStream.MoveNext(cancellationToken))
                        break;

                    var msg = _stream.ResponseStream.Current;

                    switch (msg.MessageTypeCase)
                    {
                        case EventMessage.MessageTypeOneofCase.InvokeProjectHandler:
                            await HandleProjectEventAsync(msg.InvokeProjectHandler, cancellationToken);
                            break;

                        case EventMessage.MessageTypeOneofCase.InvokeServiceHandler:
                            await HandleServiceEventAsync(msg.InvokeServiceHandler, cancellationToken);
                            break;

                        default:
                            Console.WriteLine($"[EventManager] Unhandled message type: {msg.MessageTypeCase}");
                            break;
                    }
                }
            }
            catch (RpcException ex) when (ex.StatusCode == StatusCode.Unavailable)
            {
                Console.WriteLine("[EventManager] Stream closed by server (Unavailable).");
            }
            catch (RpcException ex) when (ex.StatusCode == StatusCode.Cancelled)
            {
                Console.WriteLine("[EventManager] Stream cancelled.");
            }
        }

        public async Task AddProjectEventHandlerAsync(string eventName, Func<ProjectEventArgs, Task> handler, CancellationToken cancellationToken)
        {
            await EnsureInitializedAsync(cancellationToken);

            await _stream!.RequestStream.WriteAsync(new EventMessage
            {
                SubscribeProjectEvent = new SubscribeProjectEvent
                {
                    EventNames = { eventName }
                }
            });

            _projectHandlers[eventName] = handler;
        }

        public async Task AddServiceEventHandlerAsync(string eventName, Func<ServiceEventArgs, Task> handler, ServerEventOptions? options, CancellationToken cancellationToken)
        {
            await EnsureInitializedAsync(cancellationToken);

            options ??= new ServerEventOptions();

            await _stream!.RequestStream.WriteAsync(new EventMessage
            {
                SubscribeServiceEvent = new SubscribeServiceEvent
                {
                    EventNames = { eventName },
                    Host = options.Host ?? "",
                    Language = options.Language ?? ""
                }
            });

            _serviceHandlers[eventName] = handler;
        }

        public void RemoveProjectEventHandler(string eventName) => _projectHandlers.Remove(eventName);
        public void RemoveServiceEventHandler(string eventName) => _serviceHandlers.Remove(eventName);

        private async Task SendReadyEventAsync()
        {
            await _stream!.RequestStream.WriteAsync(new EventMessage
            {
                ExtensionReadyEvent = new ExtensionReadyEvent { Status = "ready" }
            });
        }

        private async Task SendProjectHandlerStatusAsync(string eventName, string status, string message)
        {
            await _stream!.RequestStream.WriteAsync(new EventMessage
            {
                ProjectHandlerStatus = new ProjectHandlerStatus
                {
                    EventName = eventName,
                    Status = status,
                    Message = message
                }
            });
        }

        private async Task SendServiceHandlerStatusAsync(string eventName, string serviceName, string status, string message)
        {
            await _stream!.RequestStream.WriteAsync(new EventMessage
            {
                ServiceHandlerStatus = new ServiceHandlerStatus
                {
                    EventName = eventName,
                    ServiceName = serviceName,
                    Status = status,
                    Message = message
                }
            });
        }

        private async Task HandleProjectEventAsync(InvokeProjectHandler invokeMsg, CancellationToken cancellationToken)
        {
            if (_projectHandlers.TryGetValue(invokeMsg.EventName, out var handler))
            {
                var status = "completed";
                var message = "";

                try
                {
                    await handler(new ProjectEventArgs
                    {
                        Project = invokeMsg.Project
                    });
                }
                catch (Exception ex)
                {
                    status = "failed";
                    message = ex.Message;
                    Console.WriteLine($"[ProjectHandler] Error: {ex}");
                }

                await SendProjectHandlerStatusAsync(invokeMsg.EventName, status, message);
            }
        }

        private async Task HandleServiceEventAsync(InvokeServiceHandler invokeMsg, CancellationToken cancellationToken)
        {
            if (_serviceHandlers.TryGetValue(invokeMsg.EventName, out var handler))
            {
                var status = "completed";
                var message = "";

                try
                {
                    await handler(new ServiceEventArgs
                    {
                        Project = invokeMsg.Project,
                        Service = invokeMsg.Service
                    });
                }
                catch (Exception ex)
                {
                    status = "failed";
                    message = ex.Message;
                    Console.WriteLine($"[ServiceHandler] Error: {ex}");
                }

                await SendServiceHandlerStatusAsync(invokeMsg.EventName, invokeMsg.Service.Name, status, message);
            }
        }
    }
}