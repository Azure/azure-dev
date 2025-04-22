import asyncio  
from collections import defaultdict  
import grpc  
  
class EventManager:  
    def __init__(self, azd_client):  
        self.azd_client = azd_client  
        self.stream = None  
        self.project_handlers = defaultdict(list)  
        self.service_handlers = defaultdict(list)  
  
    async def ensure_initialized(self):  
        if self.stream is None:  
            # Initialize the stream here, e.g.,  
            # self.stream = self.azd_client.events.EventStream()  
            pass  
  
    async def receive_events(self):  
        await self.ensure_initialized()  
        await self.send_ready_event()  
          
        try:  
            async for msg in self.stream:  
                if msg.HasField('invoke_project_handler'):  
                    await self.handle_project_event(msg.invoke_project_handler)  
                elif msg.HasField('invoke_service_handler'):  
                    await self.handle_service_event(msg.invoke_service_handler)  
                else:  
                    print(f"Unhandled message type: {msg}")  
        except grpc.aio.AioRpcError as e:  
            print(f"Stream closed with error: {e}")  
  
    async def add_project_event_handler(self, event_name, handler):  
        await self.ensure_initialized()  
        # Subscribe to project events here  
        # await self.stream.write(...)  
        self.project_handlers[event_name].append(handler)  
  
    async def add_service_event_handler(self, event_name, handler, options=None):  
        await self.ensure_initialized()  
        # Subscribe to service events here  
        # await self.stream.write(...)  
        self.service_handlers[event_name].append(handler)  
  
    async def send_ready_event(self):  
        # Send ready event  
        # await self.stream.write(...)  
        pass  
  
    async def handle_project_event(self, invoke_msg):  
        handlers = self.project_handlers.get(invoke_msg.event_name, [])  
        status = "completed"  
        message = ""  
  
        for handler in handlers:  
            try:  
                await handler(invoke_msg.project)  
            except Exception as ex:  
                status = "failed"  
                message = str(ex)  
                print(f"Error in project handler: {ex}")  
  
        await self.send_project_handler_status(invoke_msg.event_name, status, message)  
  
    async def handle_service_event(self, invoke_msg):  
        handlers = self.service_handlers.get(invoke_msg.event_name, [])  
        status = "completed"  
        message = ""  
  
        for handler in handlers:  
            try:  
                await handler(invoke_msg.project, invoke_msg.service)  
            except Exception as ex:  
                status = "failed"  
                message = str(ex)  
                print(f"Error in service handler: {ex}")  
  
        await self.send_service_handler_status(invoke_msg.event_name, invoke_msg.service.name, status, message)  
  
    async def send_project_handler_status(self, event_name, status, message):  
        # Send project handler status  
        # await self.stream.write(...)  
        pass  
  
    async def send_service_handler_status(self, event_name, service_name, status, message):  
        # Send service handler status  
        # await self.stream.write(...)  
        pass  