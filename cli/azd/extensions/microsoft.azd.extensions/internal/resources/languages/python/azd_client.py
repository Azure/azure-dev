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

        self.channel = grpc.intercept_channel(channel, _AuthInterceptor(access_token))
    
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


class _AuthInterceptor(grpc.UnaryUnaryClientInterceptor):

    def __init__(self, access_token):
        self._access_token = access_token

    def intercept_unary_unary(self, continuation, client_call_details, request):
        metadata = []
        if client_call_details.metadata is not None:
            metadata = list(client_call_details.metadata)
        metadata.append(('authorization', self._access_token))

        new_call_details = _ClientCallDetails(
            method=client_call_details.method,
            timeout=client_call_details.timeout,
            metadata=metadata,
            credentials=client_call_details.credentials,
            wait_for_ready=client_call_details.wait_for_ready,
            compression=client_call_details.compression,
        )
        return continuation(new_call_details, request)


class _ClientCallDetails(grpc.ClientCallDetails):

    def __init__(self, method, timeout, metadata, credentials, wait_for_ready, compression):
        self.method = method
        self.timeout = timeout
        self.metadata = metadata
        self.credentials = credentials
        self.wait_for_ready = wait_for_ready
        self.compression = compression
