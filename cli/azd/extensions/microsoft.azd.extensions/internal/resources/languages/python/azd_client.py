import os
import grpc

from generated_proto.compose_pb2_grpc import ComposeServiceStub
from generated_proto.deployment_pb2_grpc import DeploymentServiceStub
from generated_proto.environment_pb2_grpc import EnvironmentServiceStub
from generated_proto.event_pb2_grpc import EventServiceStub
from generated_proto.project_pb2_grpc import ProjectServiceStub
from generated_proto.prompt_pb2_grpc import PromptServiceStub
from generated_proto.user_config_pb2_grpc import UserConfigServiceStub
from generated_proto.workflow_pb2_grpc import WorkflowServiceStub

class AzdClient:
    def __init__(self, server_address: str, access_token: str):

        if not server_address.startswith("http"):
            server_address = "http://" + server_address

        target = server_address.replace("http://", "").replace("https://", "")

        channel = grpc.insecure_channel(target)

        # Use a comprehensive auth interceptor that handles all call types
        auth_interceptor = AuthInterceptor(access_token)
        self.channel = grpc.intercept_channel(channel, auth_interceptor)

        self.compose = ComposeServiceStub(self.channel)
        self.deployment = DeploymentServiceStub(self.channel)
        self.environment = EnvironmentServiceStub(self.channel)
        self.events = EventServiceStub(self.channel)
        self.project = ProjectServiceStub(self.channel)
        self.prompt = PromptServiceStub(self.channel)
        self.user_config = UserConfigServiceStub(self.channel)
        self.workflow = WorkflowServiceStub(self.channel)

    def close(self):
        self.channel.close()


class AuthInterceptor(
    grpc.UnaryUnaryClientInterceptor,
    grpc.UnaryStreamClientInterceptor,
    grpc.StreamUnaryClientInterceptor,
    grpc.StreamStreamClientInterceptor
):
    """Interceptor that adds authorization headers to all gRPC call types."""

    def __init__(self, access_token):
        self._access_token = access_token

    def _add_auth_metadata(self, client_call_details):
        """Helper to add authorization metadata to call details."""
        metadata = []
        if client_call_details.metadata is not None:
            metadata = list(client_call_details.metadata)

        # Add the authorization header to all requests
        metadata.append(('authorization', self._access_token))

        return ClientCallDetails(
            method=client_call_details.method,
            timeout=client_call_details.timeout,
            metadata=metadata,
            credentials=client_call_details.credentials,
            wait_for_ready=client_call_details.wait_for_ready,
            compression=client_call_details.compression,
        )

    def intercept_unary_unary(self, continuation, client_call_details, request):
        """Intercept unary-unary calls."""
        new_details = self._add_auth_metadata(client_call_details)
        return continuation(new_details, request)

    def intercept_unary_stream(self, continuation, client_call_details, request):
        """Intercept unary-stream calls."""
        new_details = self._add_auth_metadata(client_call_details)
        return continuation(new_details, request)

    def intercept_stream_unary(self, continuation, client_call_details, request_iterator):
        """Intercept stream-unary calls."""
        new_details = self._add_auth_metadata(client_call_details)
        return continuation(new_details, request_iterator)

    def intercept_stream_stream(self, continuation, client_call_details, request_iterator):
        """Intercept stream-stream (bidirectional) calls."""
        new_details = self._add_auth_metadata(client_call_details)
        return continuation(new_details, request_iterator)


class ClientCallDetails(grpc.ClientCallDetails):
    """Simple implementation of ClientCallDetails."""

    def __init__(self, method, timeout, metadata, credentials, wait_for_ready, compression):
        self.method = method
        self.timeout = timeout
        self.metadata = metadata
        self.credentials = credentials
        self.wait_for_ready = wait_for_ready
        self.compression = compression
