import asyncio
import logging
import sys
import os
import traceback

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
from azd_client import AzdClient
from event_manager import EventManager

class ListenCommand:
    def __init__(self, azd_client: AzdClient):
        self.azd_client = azd_client

    async def execute(self):
        logging.info("[ListenCommand] EventManager initialized")
        event_manager = EventManager(self.azd_client)
        # Project Event Handler: preprovision
        logging.info("[ListenCommand] Registering project.preprovision event handler")
        await event_manager.add_project_event_handler("preprovision", self.preprovision_handler)

        # Service Event Handler: prepackage
        logging.info("[ListenCommand] Registering service.prepackage event handler")
        await event_manager.add_service_event_handler("prepackage", self.prepackage_handler)

        # Start listening (blocking call)
        logging.info("[ListenCommand] Starting to listen for events...")
        await event_manager.receive()

    async def preprovision_handler(self, args):
        try:
            logging.info("[EventHandler] Starting project.preprovision work")
            # Ensure output is flushed immediately
            for i in range(1, 21):
                message = f"{i}. Doing important work in Python extension..."
                print(message)
                logging.info(f"[EventHandler] {message}")
                await asyncio.sleep(0.25)
            logging.info("[EventHandler] Finished project.preprovision work")
        except Exception as e:
            logging.error(f"[EventHandler] Error in preprovision_handler: {e}")
            traceback.print_exc()
            raise

    async def prepackage_handler(self, args):
        try:
            logging.info(f"[EventHandler] Starting service.prepackage work for service: {args.service}")
            # Ensure output is flushed immediately
            for i in range(1, 21):
                message = f"{i}. Doing important work in Python extension..."
                print(message)
                logging.info(f"[EventHandler] {message}")
                await asyncio.sleep(0.25)
            logging.info("[EventHandler] Finished service.prepackage work")
        except Exception as e:
            logging.error(f"[EventHandler] Error in prepackage_handler: {e}")
            traceback.print_exc()
            raise
