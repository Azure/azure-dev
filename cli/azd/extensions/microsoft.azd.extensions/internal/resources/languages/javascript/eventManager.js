const grpc = require("@grpc/grpc-js");
const fs = require("fs");
const path = require("path");
const os = require("os");
const logger = require("./logger");

class EventManager {
  constructor(client) {
    this._client = client;
    this._stream = null;
    this._projectHandlers = {};
    this._serviceHandlers = {};
  }

  async close() {
    if (this._stream) {
      logger.info("Closing event stream");
      return new Promise((resolve) => {
        this._stream.end();
        resolve();
      });
    }
    return Promise.resolve();
  }

  async _ensureStream() {
    if (!this._stream) {
      logger.info("Establishing event stream");
      try {
        // Log metadata right before establishing the stream
        logger.debug("Metadata being used for stream:", {
          metadata:
            this._client && this._client._metadata
              ? this._client._metadata.getMap()
              : {},
        });

        this._stream = this._client.Events.EventStream(this._client._metadata);
        logger.info("Event stream established");
        await this._sendReadyEvent();
      } catch (err) {
        const errorMsg = `Failed to establish event stream: ${err.message}`;
        logger.error(errorMsg, { error: err });
        throw err;
      }
    }
  }

  async _sendReadyEvent() {
    // Directly create the event object with its properties
    const event = {
      extension_ready_event: {
        status: "ready",
        message: "JavaScript extension is initialized and ready",
      },
    };

    // Log raw details before any transformations
    logger.debug(`Raw event to send: ${JSON.stringify(event, null, 2)}`, {
      event,
    });
    logger.debug(`Object keys: ${Object.keys(event)}`);

    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          const errorMsg = `Failed to send ready event: ${err.message}`;
          logger.error(errorMsg, { error: err });
          reject(err);
          return;
        }

        logger.info("Extension ready event sent successfully");
        resolve();
      });
    });
  }

  _listen() {
    if (!this._stream) {
      const errorMsg = "Cannot listen: stream not initialized";
      logger.error(errorMsg);
      return;
    }

    this._stream.on("data", async (msg) => {
      this._logEvent("RECV", msg);

      try {
        if (msg.invoke_project_handler) {
          await this._invokeProjectHandler(msg.invoke_project_handler);
        }

        if (msg.invoke_service_handler) {
          await this._invokeServiceHandler(msg.invoke_service_handler);
        }
      } catch (err) {
        const errorMsg = `Error processing received message: ${err.message}`;
        logger.error(errorMsg, { error: err });
      }
    });

    this._stream.on("error", (err) => {
      if (err.code === grpc.status.UNAVAILABLE) {
        logger.warn(
          `Stream error (UNAVAILABLE): ${err.message} - treating as expected`,
          { error: err }
        );
        return;
      }
      logger.error(`Stream error: ${err.message}`, { error: err });
    });

    this._stream.on("end", () => {
      logger.info("Stream ended by server (EOF)");
    });
  }

  async _invokeProjectHandler(invokeMsg) {
    const eventName = invokeMsg.event_name;
    logger.info(`Handling project event: ${eventName}`);

    const handler = this._projectHandlers[eventName];
    if (!handler) {
      logger.warn(`No handler registered for project event: ${eventName}`);
      return;
    }

    const args = {
      project: invokeMsg.project,
    };

    let status = "completed";
    let message = "";

    try {
      await handler(args);
    } catch (err) {
      status = "failed";
      message = err.message;
      logger.error(
        `invokeProjectHandler error for event ${eventName}: ${err.message}`,
        { eventName, error: err }
      );
    }

    return this._sendProjectHandlerStatus(eventName, status, message);
  }

  async _invokeServiceHandler(invokeMsg) {
    const eventName = invokeMsg.event_name;
    logger.info(`Handling service event: ${eventName}`);

    const handler = this._serviceHandlers[eventName];
    if (!handler) {
      logger.warn(`No handler registered for service event: ${eventName}`);
      return;
    }

    const args = {
      project: invokeMsg.project,
      service: invokeMsg.service,
    };

    let status = "completed";
    let message = "";

    try {
      await handler(args);
    } catch (err) {
      status = "failed";
      message = err.message;
      logger.error(
        `invokeServiceHandler error for event ${eventName}: ${err.message}`,
        { eventName, serviceName: invokeMsg.service.name, error: err }
      );
    }

    return this._sendServiceHandlerStatus(
      eventName,
      invokeMsg.service.name,
      status,
      message
    );
  }

  _sendProjectHandlerStatus(eventName, status, message) {
    // Directly create the event object with its properties
    const event = {
      project_handler_status: {
        event_name: eventName,
        status,
        message,
      },
    };

    this._logEvent("SEND", event);
    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          logger.error(`Error sending project handler status: ${err.message}`, {
            eventName,
            status,
            error: err,
          });
          reject(err);
        } else {
          resolve();
        }
      });
    });
  }

  _sendServiceHandlerStatus(eventName, serviceName, status, message) {
    // Directly create the event object with its properties
    const event = {
      service_handler_status: {
        event_name: eventName,
        service_name: serviceName,
        status,
        message,
      },
    };

    this._logEvent("SEND", event);
    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          logger.error(`Error sending service handler status: ${err.message}`, {
            eventName,
            serviceName,
            status,
            error: err,
          });
          reject(err);
        } else {
          resolve();
        }
      });
    });
  }

  async receive() {
    try {
      await this._ensureStream();
      this._listen();
      logger.info("Started receiving events");
    } catch (err) {
      const errorMsg = `Failed to start receiving events: ${err.message}`;
      logger.error(errorMsg, { error: err });
      throw err;
    }
  }

  async addProjectEventHandler(eventName, handler) {
    try {
      await this._ensureStream();
      this._projectHandlers[eventName] = handler;

      // Directly create the event object with its properties
      const event = {
        subscribe_project_event: {
          event_names: [eventName],
        },
      };

      this._logEvent("SEND", event);

      return new Promise((resolve, reject) => {
        this._stream.write(event, (err) => {
          if (err) {
            const errorMsg = `Error subscribing to project event ${eventName}: ${err.message}`;
            logger.error(errorMsg, { eventName, error: err });
            reject(err);
            return;
          }

          logger.info(`Subscribed to project event: ${eventName}`);
          resolve();
        });
      });
    } catch (err) {
      const errorMsg = `Failed to add project event handler for ${eventName}: ${err.message}`;
      logger.error(errorMsg, { eventName, error: err });
      throw err;
    }
  }

  async addServiceEventHandler(eventName, handler, options = {}) {
    try {
      await this._ensureStream();
      this._serviceHandlers[eventName] = handler;

      // Directly create the event object with its properties
      const event = {
        subscribe_service_event: {
          event_names: [eventName],
          host: options.host || "",
          language: options.language || "",
        },
      };

      this._logEvent("SEND", event);

      return new Promise((resolve, reject) => {
        this._stream.write(event, (err) => {
          if (err) {
            const errorMsg = `Error subscribing to service event ${eventName}: ${err.message}`;
            logger.error(errorMsg, { eventName, options, error: err });
            reject(err);
            return;
          }

          logger.info(`Subscribed to service event: ${eventName}`);
          resolve();
        });
      });
    } catch (err) {
      const errorMsg = `Failed to add service event handler for ${eventName}: ${err.message}`;
      logger.error(errorMsg, { eventName, error: err });
      throw err;
    }
  }

  removeProjectEventHandler(eventName) {
    logger.info(`Removing project event handler: ${eventName}`);
    delete this._projectHandlers[eventName];
  }

  removeServiceEventHandler(eventName) {
    logger.info(`Removing service event handler: ${eventName}`);
    delete this._serviceHandlers[eventName];
  }

  // Fully generic logging function with no hardcoded event types
  _logEvent(direction, event) {
    // Find the first defined field in the event object (which represents the event type)
    const entries = Object.entries(event);
    if (entries.length === 0) {
      logger.warn(`${direction} Empty event object`);
      return;
    }

    // The first non-null field is our event type
    const [eventType, eventData] = entries[0];

    // Convert snake_case to PascalCase for type name
    const typeName = eventType
      .split("_")
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
      .join("");

    // Extract key information from the event data for summary logging
    let details = "";
    try {
      // Look for common fields that might contain identifying information
      const detailFields = [
        "event_name",
        "status",
        "event_names",
        "name",
        "service_name",
      ];
      const extractedDetails = [];

      for (const field of detailFields) {
        if (eventData[field]) {
          if (Array.isArray(eventData[field])) {
            extractedDetails.push(`${field}: ${eventData[field].join(", ")}`);
          } else {
            extractedDetails.push(`${field}: ${eventData[field]}`);
          }
        }
      }

      // Add service name if nested
      if (eventData.service?.name) {
        extractedDetails.push(`service: ${eventData.service.name}`);
      }

      details = extractedDetails.join(", ");
    } catch (err) {
      details = "[Error extracting details]";
      logger.error("Error extracting event details", { error: err });
    }

    // Log summary with event type and details
    logger.info(`${direction} ${typeName} - ${details}`);

    // Log full details at debug level
    logger.debug(`${direction} ${typeName} DETAILS`, {
      direction,
      typeName,
      eventData: JSON.stringify(eventData),
    });
  }
}

module.exports = { EventManager };
