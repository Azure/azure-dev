import asyncio
import logging
import threading
import time
import os
import sys
import datetime
from typing import Callable, Dict, Optional, Iterator
import queue
import grpc
from azd_client import AzdClient
import event_pb2

# Get logger - the actual configuration is done in main.py
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
        self._lock = threading.RLock()
        self._messages = queue.Queue()
        self._stop_flag = threading.Event()

        self._project_handlers: Dict[str, Callable[[ProjectEventArgs], asyncio.Future]] = {}
        self._service_handlers: Dict[str, Callable[[ServiceEventArgs], asyncio.Future]] = {}

        logger.info("[EventManager] Initialized")

    async def dispose(self):
        logger.info("[EventManager] Disposing event manager...")
        self._stop_flag.set()

        # Properly close the stream
        if self._stream:
            try:
                self._stream.cancel()
                logger.info("[EventManager] Stream cancelled")
            except Exception as ex:
                logger.warning(f"[EventManager] Error while closing stream: {ex}")

        logger.info("[EventManager] Event manager disposed")

    async def ensure_initialized(self):
        """Initialize the gRPC stream if not already initialized."""
        with self._lock:
            if self._stream is None:
                logger.info("[EventManager] Initializing gRPC stream...")
                try:
                    # Create the bidirectional stream - simple approach like Go
                    self._stream = self._azd_client.events.EventStream.__call__(self._request_iterator())
                    logger.info(f"[EventManager] EventStream initialized successfully: {type(self._stream)}")
                except Exception as ex:
                    logger.exception(f"[EventManager] Failed to initialize stream: {ex}")
                    raise

    def _request_iterator(self):
        """Synchronous iterator to provide messages to gRPC."""
        logger.info("[EventManager] Starting request iterator")
        while not self._stop_flag.is_set():
            try:
                # Block until a message is available or timeout
                try:
                    message = self._messages.get(block=True, timeout=0.5)
                    # Log what we're sending
                    logger.info(f"[EventManager] Iterator yielding message: {message.WhichOneof('message_type')}")
                    yield message
                    self._messages.task_done()
                except queue.Empty:
                    # Just continue the loop if no messages
                    continue
            except Exception as ex:
                if not self._stop_flag.is_set():
                    logger.exception(f"[EventManager] Error in request iterator: {ex}")
        logger.info("[EventManager] Request iterator stopped")

    async def _send_message(self, message):
        """Queue a message to be sent to the gRPC stream."""
        # Ensure stream is initialized
        await self.ensure_initialized()

        # Log what we're sending
        message_type = message.WhichOneof('message_type')
        logger.info(f"[EventManager] Queueing message: {message_type}")

        # Add to the queue
        self._messages.put(message)

        # Wait a bit for the message to be sent
        await asyncio.sleep(0.1)

    async def receive(self):
        """Start receiving messages from the stream."""
        await self.ensure_initialized()

        # Send ready event immediately
        await self.send_ready_event()

        logger.info("[EventManager] Listening for events...")
        try:
            # Process incoming messages using a standard loop like Go
            while not self._stop_flag.is_set():
                try:
                    # This will block until a message is received
                    message = await asyncio.to_thread(next, self._stream, None)
                    if message is None:
                        # End of stream
                        break

                    logger.info(f"[EventManager] Received message: {message.WhichOneof('message_type')}")

                    # Process the message
                    if message.HasField("invoke_project_handler"):
                        await self.handle_project_event(message.invoke_project_handler)
                    elif message.HasField("invoke_service_handler"):
                        await self.handle_service_event(message.invoke_service_handler)
                    else:
                        logger.warning(f"[EventManager] Unhandled message type: {message.WhichOneof('message_type')}")
                except StopIteration:
                    # End of stream reached
                    logger.info("[EventManager] End of stream reached")
                    break
                except grpc.RpcError as rpc_error:
                    # Check for expected closure cases like in Go
                    if rpc_error.code() == grpc.StatusCode.CANCELLED:
                        logger.info("[EventManager] Stream cancelled (expected)")
                        break
                    elif rpc_error.code() == grpc.StatusCode.UNAVAILABLE:
                        logger.info("[EventManager] Stream unavailable (server closed)")
                        break
                    else:
                        logger.exception(f"[EventManager] Unexpected gRPC error: {rpc_error}")
                        break
                except Exception as ex:
                    if self._stop_flag.is_set():
                        break
                    logger.exception(f"[EventManager] Error processing message: {ex}")
                    # Brief pause to avoid tight loop on errors
                    await asyncio.sleep(0.1)
        except Exception as ex:
            logger.exception(f"[EventManager] Unexpected error in receive: {ex}")
        finally:
            logger.info("[EventManager] Receive loop exiting")

    async def add_project_event_handler(self, event_name: str, handler: Callable[[ProjectEventArgs], asyncio.Future]):
        """Add a handler for project events."""
        logger.info(f"[EventManager] Adding project event handler: {event_name}")
        self._project_handlers[event_name] = handler

        # Send subscribe message - follow Go pattern exactly
        message = event_pb2.EventMessage(
            subscribe_project_event=event_pb2.SubscribeProjectEvent(
                event_names=[event_name]
            )
        )

        # Send message directly through queue
        await self._send_message(message)
        logger.info(f"[EventManager] Project event handler registered: {event_name}")

    async def add_service_event_handler(self, event_name: str, handler: Callable[[ServiceEventArgs], asyncio.Future],
                                     options: Optional[ServerEventOptions] = None):
        """Add a handler for service events."""
        options = options or ServerEventOptions()
        logger.info(f"[EventManager] Adding service event handler: {event_name} (host={options.host}, language={options.language})")
        self._service_handlers[event_name] = handler

        # Send subscribe message - follow Go pattern exactly
        message = event_pb2.EventMessage(
            subscribe_service_event=event_pb2.SubscribeServiceEvent(
                event_names=[event_name],
                host=options.host or "",
                language=options.language or ""
            )
        )

        # Send message directly through queue
        await self._send_message(message)
        logger.info(f"[EventManager] Service event handler registered: {event_name}")

    def remove_project_event_handler(self, event_name: str):
        """Remove a project event handler."""
        logger.info(f"[EventManager] Removing project handler: {event_name}")
        self._project_handlers.pop(event_name, None)

    def remove_service_event_handler(self, event_name: str):
        """Remove a service event handler."""
        logger.info(f"[EventManager] Removing service handler: {event_name}")
        self._service_handlers.pop(event_name, None)

    async def send_ready_event(self):
        """Send the ready event to signal the extension is ready to receive events."""
        logger.info("[EventManager] Sending ExtensionReadyEvent")

        # Create ready event exactly as Go does
        message = event_pb2.EventMessage(
            extension_ready_event=event_pb2.ExtensionReadyEvent(
                status="ready"
            )
        )

        # Send message and log success
        await self._send_message(message)
        logger.info("[EventManager] ExtensionReadyEvent sent")

    async def send_project_handler_status(self, event_name: str, status: str, message: str):
        """Send status of project event handling."""
        logger.info(f"[EventManager] Sending ProjectHandlerStatus: {event_name} => {status}")

        # Create status message like Go
        status_message = event_pb2.EventMessage(
            project_handler_status=event_pb2.ProjectHandlerStatus(
                event_name=event_name,
                status=status,
                message=message
            )
        )

        # Send it to the stream
        await self._send_message(status_message)

    async def send_service_handler_status(self, event_name: str, service_name: str, status: str, message: str):
        """Send status of service event handling."""
        logger.info(f"[EventManager] Sending ServiceHandlerStatus: {event_name}/{service_name} => {status}")

        # Create status message like Go
        status_message = event_pb2.EventMessage(
            service_handler_status=event_pb2.ServiceHandlerStatus(
                event_name=event_name,
                service_name=service_name,
                status=status,
                message=message
            )
        )

        # Send it to the stream
        await self._send_message(status_message)

    async def handle_project_event(self, invoke_msg):
        """Handle a project event from the server."""
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
        """Handle a service event from the server."""
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