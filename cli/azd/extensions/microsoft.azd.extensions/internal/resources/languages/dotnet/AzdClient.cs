using Grpc.Net.Client;
using Grpc.Core;
using Grpc.Core.Interceptors;

namespace Microsoft.Azd{

    public class AzdClient : IDisposable
    {
        private readonly GrpcChannel _channel;

        // Expose your gRPC service clients here
        public ComposeService.ComposeServiceClient Compose { get; }
        public DeploymentService.DeploymentServiceClient Deployment { get; }
        public EnvironmentService.EnvironmentServiceClient Environment { get; }
        public EventService.EventServiceClient Events { get; }
        public ProjectService.ProjectServiceClient Project { get; }
        public PromptService.PromptServiceClient Prompt { get; }
        public UserConfigService.UserConfigServiceClient UserConfig { get; }
        public WorkflowService.WorkflowServiceClient Workflow { get; }

        public AzdClient() : this(
            System.Environment.GetEnvironmentVariable("AZD_SERVER"), 
            System.Environment.GetEnvironmentVariable("AZD_ACCESS_TOKEN"))
        {
        }

        public AzdClient(string serverAddress, string accessToken)
        {
            if (string.IsNullOrEmpty(serverAddress))
            {
                throw new ArgumentNullException(nameof(serverAddress), "Server address cannot be null or empty.");
            }

            if (string.IsNullOrEmpty(accessToken))
            {
                throw new ArgumentNullException(nameof(accessToken), "Access token cannot be null or empty.");
            }

            if (!serverAddress.StartsWith("http"))
            {
                serverAddress = "http://" + serverAddress;
            }

            _channel = GrpcChannel.ForAddress(serverAddress);
            var callInvoker = _channel.Intercept(new AuthHeaderInterceptor(accessToken));

            // Initialize gRPC service clients
            Compose = new ComposeService.ComposeServiceClient(callInvoker);
            Deployment = new DeploymentService.DeploymentServiceClient(callInvoker);
            Environment = new EnvironmentService.EnvironmentServiceClient(callInvoker);
            Events = new EventService.EventServiceClient(callInvoker);
            Project = new ProjectService.ProjectServiceClient(callInvoker);
            Prompt = new PromptService.PromptServiceClient(callInvoker);
            UserConfig = new UserConfigService.UserConfigServiceClient(callInvoker);
            Workflow = new WorkflowService.WorkflowServiceClient(callInvoker);
        }

        public void Dispose()
        {
            _channel?.Dispose();
        }
    }

    public class AuthHeaderInterceptor : Interceptor
    {
        private readonly string _accessToken;
        public AuthHeaderInterceptor(string accessToken)
        {
            if (string.IsNullOrEmpty(accessToken))
            {
                throw new ArgumentNullException(nameof(accessToken), "Access token cannot be null or empty.");
            }

            _accessToken = accessToken;
        }

        public override AsyncUnaryCall<TResponse> AsyncUnaryCall<TRequest, TResponse>(
            TRequest request,
            ClientInterceptorContext<TRequest, TResponse> context,
            AsyncUnaryCallContinuation<TRequest, TResponse> continuation)
        {
            var headers = context.Options.Headers ?? new Metadata();
            headers.Add("authorization", _accessToken);

            var optionsWithAuth = context.Options.WithHeaders(headers);
            var updatedContext = new ClientInterceptorContext<TRequest, TResponse>(
                context.Method, context.Host, optionsWithAuth);

            return base.AsyncUnaryCall(request, updatedContext, continuation);
        }

        public override AsyncServerStreamingCall<TResponse> AsyncServerStreamingCall<TRequest, TResponse>(
            TRequest request,
            ClientInterceptorContext<TRequest, TResponse> context,
            AsyncServerStreamingCallContinuation<TRequest, TResponse> continuation)
        {
            var headers = context.Options.Headers ?? new Metadata();
            headers.Add("authorization", _accessToken);

            var optionsWithAuth = context.Options.WithHeaders(headers);
            var updatedContext = new ClientInterceptorContext<TRequest, TResponse>(
                context.Method, context.Host, optionsWithAuth);

            return base.AsyncServerStreamingCall(request, updatedContext, continuation);
        }

        public override AsyncClientStreamingCall<TRequest, TResponse> AsyncClientStreamingCall<TRequest, TResponse>(
            ClientInterceptorContext<TRequest, TResponse> context,
            AsyncClientStreamingCallContinuation<TRequest, TResponse> continuation)
        {
            var headers = context.Options.Headers ?? new Metadata();
            headers.Add("authorization", _accessToken);

            var optionsWithAuth = context.Options.WithHeaders(headers);
            var updatedContext = new ClientInterceptorContext<TRequest, TResponse>(
                context.Method, context.Host, optionsWithAuth);

            return base.AsyncClientStreamingCall(updatedContext, continuation);
        }

        public override AsyncDuplexStreamingCall<TRequest, TResponse> AsyncDuplexStreamingCall<TRequest, TResponse>(
            ClientInterceptorContext<TRequest, TResponse> context,
            AsyncDuplexStreamingCallContinuation<TRequest, TResponse> continuation)
        {
            var headers = context.Options.Headers ?? new Metadata();
            headers.Add("authorization", _accessToken);

            var optionsWithAuth = context.Options.WithHeaders(headers);
            var updatedContext = new ClientInterceptorContext<TRequest, TResponse>(
                context.Method, context.Host, optionsWithAuth);

            return base.AsyncDuplexStreamingCall(updatedContext, continuation);
        }
    }
}
