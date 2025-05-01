const grpc = require("@grpc/grpc-js");
const fs = require("fs");
const path = require("path");
const os = require("os");

class EventManager {
  constructor(client) {
    this._client = client;
    this._stream = null;
    this._projectHandlers = {};
    this._serviceHandlers = {};
    this._setupLogger();
  }

  _setupLogger() {
    // Prioritize finding the executable path for log placement
    const possibleLogDirs = [
      // First priority: executable directory (where azd.exe runs from)
      path.dirname(process.execPath),
      // Second priority: module directory (where the JS file is)
      __dirname,
      // Fallbacks if neither of above are writable
      os.homedir(),
      os.tmpdir(),
    ];

    for (const dir of possibleLogDirs) {
      try {
        const logPath = path.join(dir, "eventManager.log");
        fs.appendFileSync(
          logPath,
          `[${new Date().toISOString()}] EventManager log initialized\n`
        );
        this._logFilePath = logPath;
        // Keep one console.log to show where logs are being written
        console.log(`EventManager logging to: ${this._logFilePath}`);
        break;
      } catch (err) {
        // Don't log this error as we're still trying to establish logging
      }
    }

    if (!this._logFilePath) {
      // If we can't log anywhere, at least print to console
      console.error(
        "WARNING: Could not create log file in any location. Logging disabled."
      );
    } else {
      this._writeToLog(`EventManager fully initialized`);
    }
  }

  _writeToLog(message) {
    if (!this._logFilePath) {
      return;
    }

    const timestamp = new Date().toISOString();
    const logEntry = `[${timestamp}] ${message}${os.EOL}`;

    try {
      fs.appendFileSync(this._logFilePath, logEntry);
    } catch (err) {
      console.error(`Failed to write to log file: ${err.message}`);
      this._logFilePath = null;
    }
  }

  // Fully generic logging function with no hardcoded event types
  _logEvent(direction, event) {
    // Find the first defined field in the event object (which represents the event type)
    const entries = Object.entries(event);
    if (entries.length === 0) {
      this._writeToLog(`${direction} Empty event object`);
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
    }

    // Log summary with event type and details
    this._writeToLog(`${direction} ${typeName} - ${details}`);

    // Always log full details
    this._writeToLog(
      `${direction} ${typeName} DETAILS: ${JSON.stringify(eventData)}`
    );
  }

  async close() {
    if (this._stream) {
      this._writeToLog("Closing event stream");
      return new Promise((resolve) => {
        this._stream.end();
        resolve();
      });
    }
    return Promise.resolve();
  }

  async _ensureStream() {
    if (!this._stream) {
      this._writeToLog("Establishing event stream");
      try {
        // Log metadata again right before establishing the stream
        this._writeToLog("Metadata being used for stream:");
        if (this._client && this._client._metadata) {
          Object.entries(this._client._metadata.getMap()).forEach(
            ([key, value]) => {
              this._writeToLog(`  ${key}: ${value}`);
            }
          );
        }

        this._stream = this._client.Events.EventStream(this._client._metadata);
        this._writeToLog("Event stream established");
        await this._sendReadyEvent();
      } catch (err) {
        const errorMsg = `Failed to establish event stream: ${err.message}`;
        this._writeToLog(errorMsg);
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
    this._writeToLog(`Raw event to send: ${JSON.stringify(event, null, 2)}`);
    this._writeToLog(`Object keys: ${Object.keys(event)}`);

    return new Promise((resolve, reject) => {
      this._stream.write(event, (err) => {
        if (err) {
          const errorMsg = `Failed to send ready event: ${err.message}`;
          this._writeToLog(errorMsg);
          reject(err);
          return;
        }

        this._writeToLog("Extension ready event sent successfully");
        resolve();
      });
    });
  }

  _listen() {
    if (!this._stream) {
      const errorMsg = "Cannot listen: stream not initialized";
      this._writeToLog(errorMsg);
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
        this._writeToLog(errorMsg);
      }
    });

    this._stream.on("error", (err) => {
      if (err.code === grpc.status.UNAVAILABLE) {
        this._writeToLog(
          `Stream error (UNAVAILABLE): ${err.message} - treating as expected`
        );
        return;
      }
      this._writeToLog(`Stream error: ${err.message}`);
    });

    this._stream.on("end", () => {
      this._writeToLog("Stream ended by server (EOF)");
    });
  }

  async _invokeProjectHandler(invokeMsg) {
    const eventName = invokeMsg.event_name;
    this._writeToLog(`Handling project event: ${eventName}`);

    const handler = this._projectHandlers[eventName];
    if (!handler) {
      this._writeToLog(`No handler registered for project event: ${eventName}`);
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
      this._writeToLog(
        `invokeProjectHandler error for event ${eventName}: ${err.message}`
      );
    }

    return this._sendProjectHandlerStatus(eventName, status, message);
  }

  async _invokeServiceHandler(invokeMsg) {
    const eventName = invokeMsg.event_name;
    this._writeToLog(`Handling service event: ${eventName}`);

    const handler = this._serviceHandlers[eventName];
    if (!handler) {
      this._writeToLog(`No handler registered for service event: ${eventName}`);
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
      this._writeToLog(
        `invokeServiceHandler error for event ${eventName}: ${err.message}`
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
          this._writeToLog(
            `Error sending project handler status: ${err.message}`
          );
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
          this._writeToLog(
            `Error sending service handler status: ${err.message}`
          );
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
      this._writeToLog("Started receiving events");
    } catch (err) {
      const errorMsg = `Failed to start receiving events: ${err.message}`;
      this._writeToLog(errorMsg);
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
            this._writeToLog(errorMsg);
            reject(err);
            return;
          }

          this._writeToLog(`Subscribed to project event: ${eventName}`);
          resolve();
        });
      });
    } catch (err) {
      const errorMsg = `Failed to add project event handler for ${eventName}: ${err.message}`;
      this._writeToLog(errorMsg);
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
            this._writeToLog(errorMsg);
            reject(err);
            return;
          }

          this._writeToLog(`Subscribed to service event: ${eventName}`);
          resolve();
        });
      });
    } catch (err) {
      const errorMsg = `Failed to add service event handler for ${eventName}: ${err.message}`;
      this._writeToLog(errorMsg);
      throw err;
    }
  }

  removeProjectEventHandler(eventName) {
    this._writeToLog(`Removing project event handler: ${eventName}`);
    delete this._projectHandlers[eventName];
  }

  removeServiceEventHandler(eventName) {
    this._writeToLog(`Removing service event handler: ${eventName}`);
    delete this._serviceHandlers[eventName];
  }
}

module.exports = { EventManager };
