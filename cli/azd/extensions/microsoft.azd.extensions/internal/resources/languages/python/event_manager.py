import asyncio
import logging
from typing import Callable, Dict, Optional
import grpc
from azd_client import AzdClient
import event_pb2

logger = logging.getLogger(__name__)

class ProjectEventArgs:
    def __init__(self, project):
        self.project = project

class ServiceEventArgs:
    def __init__(self, project, service):
        self.project = project
        self.service = service

class ServerEventOptions:
    def __init__(self, host: Optional[str] = None, language: Optional[str] = None):
        self.host = host
        self.language = language

class EventManager:
    def __init__(self, azd_client: AzdClient):
        self._azd_client = azd_client
        self._stream = None
        self._send_queue = asyncio.Queue()  
        
        self._project_handlers: Dict[str, Callable[[ProjectEventArgs], asyncio.Future]] = {}
        self._service_handlers: Dict[str, Callable[[ServiceEventArgs], asyncio.Future]] = {}

    async def dispose(self):
        if self._stream:
            logger.info("[EventManager] Disposing stream...")
            await self._send_queue.put(None) 
            await self._stream.done_writing()

    async def ensure_initialized(self):
        if not self._stream:
            logger.info("[EventManager] Initializing gRPC stream...")
            self._stream = self._azd_client.events.EventStream()
            logger.info("[EventManager] EventStream initialized.")

    async def receive(self):
        await self.ensure_initialized()
        await self.send_ready_event()

        logger.info("[EventManager] Listening for events...")
        try:
            async for msg in self._stream:
                logger.info(f"[EventManager] Received message type: {msg.WhichOneof('message_type')}")
                if msg.HasField("invoke_project_handler"):
                    await self.handle_project_event(msg.invoke_project_handler)
                elif msg.HasField("invoke_service_handler"):
                    await self.handle_service_event(msg.invoke_service_handler)
                else:
                    logger.warning(f"[EventManager] Unhandled message type: {msg.WhichOneof('message_type')}")
        except grpc.aio.AioRpcError as ex:
            if ex.code() == grpc.StatusCode.UNAVAILABLE:
                logger.info("[EventManager] Stream closed by server (Unavailable).")
            elif ex.code() == grpc.StatusCode.CANCELLED:
                logger.info("[EventManager] Stream cancelled.")
            else:
                logger.exception("[EventManager] Unexpected error", exc_info=ex)

    async def add_project_event_handler(self, event_name: str, handler: Callable[[ProjectEventArgs], asyncio.Future]):
        await self.ensure_initialized()
        logger.info(f"[EventManager] Subscribing to project event: {event_name}")
        await self._send_queue.put(event_pb2.EventMessage(
            subscribe_project_event=event_pb2.SubscribeProjectEvent(event_names=[event_name])
        ))
        self._project_handlers[event_name] = handler
        logger.info(f"[EventManager] Project event handler registered: {event_name}")

    async def add_service_event_handler(self, event_name: str, handler: Callable[[ServiceEventArgs], asyncio.Future],
                                        options: Optional[ServerEventOptions] = None):
        await self.ensure_initialized()
        options = options or ServerEventOptions()
        logger.info(f"[EventManager] Subscribing to service event: {event_name} (host={options.host}, language={options.language})")
        await self._send_queue.put(event_pb2.EventMessage(
            subscribe_service_event=event_pb2.SubscribeServiceEvent(
                event_names=[event_name],
                host=options.host or "",
                language=options.language or ""
            )
        ))
        self._service_handlers[event_name] = handler
        logger.info(f"[EventManager] Service event handler registered: {event_name}")

    def remove_project_event_handler(self, event_name: str):
        logger.info(f"[EventManager] Removing project handler: {event_name}")
        self._project_handlers.pop(event_name, None)

    def remove_service_event_handler(self, event_name: str):
        logger.info(f"[EventManager] Removing service handler: {event_name}")
        self._service_handlers.pop(event_name, None)

    async def send_ready_event(self):
        logger.info("[EventManager] Sending ExtensionReadyEvent")
        await self._send_queue.put(event_pb2.EventMessage(
            extension_ready_event=event_pb2.ExtensionReadyEvent(status="ready")
        ))

    async def send_project_handler_status(self, event_name: str, status: str, message: str):
        logger.info(f"[EventManager] Sending ProjectHandlerStatus: {event_name} => {status}")
        await self._send_queue.put(event_pb2.EventMessage(
            project_handler_status=event_pb2.ProjectHandlerStatus(
                event_name=event_name,
                status=status,
                message=message
            )
        ))

    async def send_service_handler_status(self, event_name: str, service_name: str, status: str, message: str):
        logger.info(f"[EventManager] Sending ServiceHandlerStatus: {event_name}/{service_name} => {status}")
        await self._send_queue.put(event_pb2.EventMessage(
            service_handler_status=event_pb2.ServiceHandlerStatus(
                event_name=event_name,
                service_name=service_name,
                status=status,
                message=message
            )
        ))

    async def handle_project_event(self, invoke_msg):
        event_name = invoke_msg.event_name
        logger.info(f"[EventManager] Handling project event: {event_name}")
        handler = self._project_handlers.get(event_name)
        status, message = "completed", ""

        if handler:
            try:
                await handler(ProjectEventArgs(invoke_msg.project))
            except Exception as ex:
                status = "failed"
                message = str(ex)
                logger.exception(f"[ProjectHandler] Error: {ex}")
            await self.send_project_handler_status(event_name, status, message)
        else:
            logger.warning(f"[EventManager] No project handler registered for event: {event_name}")

    async def handle_service_event(self, invoke_msg):
        event_name = invoke_msg.event_name
        logger.info(f"[EventManager] Handling service event: {event_name}")
        handler = self._service_handlers.get(event_name)
        status, message = "completed", ""

        if handler:
            try:
                await handler(ServiceEventArgs(invoke_msg.project, invoke_msg.service))
            except Exception as ex:
                status = "failed"
                message = str(ex)
                logger.exception(f"[ServiceHandler] Error: {ex}")
            await self.send_service_handler_status(event_name, invoke_msg.service.name, status, message)
        else:
            logger.warning(f"[EventManager] No service handler registered for event: {event_name}")