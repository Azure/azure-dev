const grpc = require("@grpc/grpc-js");
const logger = require("./logger");
const {
  EventMessage,
  ExtensionReadyEvent,
  SubscribeProjectEvent,
  SubscribeServiceEvent,
  ProjectHandlerStatus,
  ServiceHandlerStatus,
} = require("./generated/proto/event_pb");

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
        logger.debug("Metadata being used for stream:", {
          metadata:
            this._client && this._client._metadata
              ? this._client._metadata.getMap()
              : {},
        });

        this._stream = this._client.Events.eventStream(this._client._metadata);
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
    const event = new EventMessage();
    const payload = new ExtensionReadyEvent();
    payload.setStatus("ready");
    payload.setMessage("JavaScript extension is initialized and ready");

    event.setExtensionReadyEvent(payload);
    this._logEvent("SEND", event.toObject());

    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          logger.error(`Failed to send ready event: ${err.message}`, { error: err });
          reject(err);
        } else {
          logger.info("Extension ready event sent successfully");
          resolve();
        }
      });
    });
  }

  _listen() {
    if (!this._stream) {
      logger.error("Cannot listen: stream not initialized");
      return;
    }

    this._stream.on("data", async (msg) => {
      this._logEvent("RECV", msg.toObject());

      try {
        if (msg.hasInvokeProjectHandler()) {
          await this._invokeProjectHandler(msg.getInvokeProjectHandler());
        }

        if (msg.hasInvokeServiceHandler()) {
          await this._invokeServiceHandler(msg.getInvokeServiceHandler());
        }
      } catch (err) {
        logger.error(`Error processing received message: ${err.message}`, {
          error: err,
        });
      }
    });

    this._stream.on("error", (err) => {
      if (err.code === grpc.status.UNAVAILABLE) {
        logger.warn(`Stream error (UNAVAILABLE): ${err.message}`, { error: err });
        return;
      }
      logger.error(`Stream error: ${err.message}`, { error: err });
    });

    this._stream.on("end", () => {
      logger.info("Stream ended by server (EOF)");
    });
  }

  async _invokeProjectHandler(invokeMsg) {
    const eventName = invokeMsg.getEventName();
    logger.info(`Handling project event: ${eventName}`);

    const handler = this._projectHandlers[eventName];
    if (!handler) {
      logger.warn(`No handler registered for project event: ${eventName}`);
      return;
    }

    const args = { project: invokeMsg.getProject()?.toObject() };

    let status = "completed";
    let message = "";

    try {
      await handler(args);
    } catch (err) {
      status = "failed";
      message = err.message;
      logger.error(`invokeProjectHandler error for event ${eventName}: ${err.message}`, {
        eventName,
        error: err,
      });
    }

    return this._sendProjectHandlerStatus(eventName, status, message);
  }

  async _invokeServiceHandler(invokeMsg) {
    const eventName = invokeMsg.getEventName();
    logger.info(`Handling service event: ${eventName}`);

    const handler = this._serviceHandlers[eventName];
    if (!handler) {
      logger.warn(`No handler registered for service event: ${eventName}`);
      return;
    }

    const args = {
      project: invokeMsg.getProject()?.toObject(),
      service: invokeMsg.getService()?.toObject(),
    };

    let status = "completed";
    let message = "";

    try {
      await handler(args);
    } catch (err) {
      status = "failed";
      message = err.message;
      logger.error(`invokeServiceHandler error for event ${eventName}: ${err.message}`, {
        eventName,
        serviceName: invokeMsg.getService()?.getName(),
        error: err,
      });
    }

    return this._sendServiceHandlerStatus(
      eventName,
      invokeMsg.getService()?.getName() || "",
      status,
      message
    );
  }

  _sendProjectHandlerStatus(eventName, status, message) {
    const event = new EventMessage();
    const statusMsg = new ProjectHandlerStatus();

    statusMsg.setEventName(eventName);
    statusMsg.setStatus(status);
    statusMsg.setMessage(message);

    event.setProjectHandlerStatus(statusMsg);
    this._logEvent("SEND", event.toObject());

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
    const event = new EventMessage();
    const statusMsg = new ServiceHandlerStatus();

    statusMsg.setEventName(eventName);
    statusMsg.setServiceName(serviceName);
    statusMsg.setStatus(status);
    statusMsg.setMessage(message);

    event.setServiceHandlerStatus(statusMsg);
    this._logEvent("SEND", event.toObject());

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
      logger.error(`Failed to start receiving events: ${err.message}`, { error: err });
      throw err;
    }
  }

  async addProjectEventHandler(eventName, handler) {
    await this._ensureStream();
    this._projectHandlers[eventName] = handler;

    const event = new EventMessage();
    const sub = new SubscribeProjectEvent();
    sub.setEventNamesList([eventName]);
    event.setSubscribeProjectEvent(sub);

    this._logEvent("SEND", event.toObject());

    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          logger.error(`Error subscribing to project event ${eventName}: ${err.message}`, {
            eventName,
            error: err,
          });
          reject(err);
        } else {
          logger.info(`Subscribed to project event: ${eventName}`);
          resolve();
        }
      });
    });
  }

  async addServiceEventHandler(eventName, handler, options = {}) {
    await this._ensureStream();
    this._serviceHandlers[eventName] = handler;

    const event = new EventMessage();
    const sub = new SubscribeServiceEvent();
    sub.setEventNamesList([eventName]);
    sub.setHost(options.host || "");
    sub.setLanguage(options.language || "");
    event.setSubscribeServiceEvent(sub);

    this._logEvent("SEND", event.toObject());

    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          logger.error(`Error subscribing to service event ${eventName}: ${err.message}`, {
            eventName,
            options,
            error: err,
          });
          reject(err);
        } else {
          logger.info(`Subscribed to service event: ${eventName}`);
          resolve();
        }
      });
    });
  }

  removeProjectEventHandler(eventName) {
    logger.info(`Removing project event handler: ${eventName}`);
    delete this._projectHandlers[eventName];
  }

  removeServiceEventHandler(eventName) {
    logger.info(`Removing service event handler: ${eventName}`);
    delete this._serviceHandlers[eventName];
  }

  _logEvent(direction, eventObj) {
    const entries = Object.entries(eventObj);
    if (entries.length === 0) {
      logger.warn(`${direction} Empty event object`);
      return;
    }

    const [eventType, eventData] = entries[0];
    const typeName = eventType
      .split("_")
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
      .join("");

    let details = "";
    try {
      const detailFields = [
        "event_name",
        "status",
        "event_names",
        "name",
        "service_name",
      ];
      const extracted = [];

      for (const field of detailFields) {
        if (eventData[field]) {
          if (Array.isArray(eventData[field])) {
            extracted.push(`${field}: ${eventData[field].join(", ")}`);
          } else {
            extracted.push(`${field}: ${eventData[field]}`);
          }
        }
      }

      if (eventData.service?.name) {
        extracted.push(`service: ${eventData.service.name}`);
      }

      details = extracted.join(", ");
    } catch (err) {
      details = "[Error extracting details]";
      logger.error("Error extracting event details", { error: err });
    }

    logger.info(`${direction} ${typeName} - ${details}`);
    logger.debug(`${direction} ${typeName} DETAILS`, {
      direction,
      typeName,
      eventData,
    });
  }
}

module.exports = { EventManager };
