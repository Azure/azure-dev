const grpc = require('@grpc/grpc-js');

class EventManager {
  constructor(client) {
    this._client = client;
    this._stream = null;
    this._projectHandlers = {};
    this._serviceHandlers = {};
  }

  async _ensureStream() {
    if (!this._stream) {
      this._stream = this._client.Events.EventStream(this._client._metadata);

      // Send ExtensionReadyEvent
      await new Promise((resolve) => {
        this._stream.write({ extensionReadyEvent: { status: 'ready' } });
        resolve();
      });

      console.log('[EventManager] Extension ready event sent.');
      this._listen();
    }
  }

  _listen() {
    this._stream.on('data', async (msg) => {
      console.log('[EventManager] Received event:', JSON.stringify(msg));

      if (msg.invokeProjectHandler) {
        const eventName = msg.invokeProjectHandler.eventName;
        console.log(`[EventManager] Handling project event: ${eventName}`);
        const handler = this._projectHandlers[eventName];
        if (handler) {
          try {
            await handler({ project: msg.invokeProjectHandler.project });
            this._stream.write({
              projectHandlerStatus: {
                eventName,
                status: 'completed',
                message: '',
              },
            });
          } catch (err) {
            this._stream.write({
              projectHandlerStatus: {
                eventName,
                status: 'failed',
                message: err.message,
              },
            });
          }
        }
      }

      if (msg.invokeServiceHandler) {
        const eventName = msg.invokeServiceHandler.eventName;
        console.log(`[EventManager] Handling service event: ${eventName}`);
        const handler = this._serviceHandlers[eventName];
        if (handler) {
          try {
            await handler({
              project: msg.invokeServiceHandler.project,
              service: msg.invokeServiceHandler.service,
            });
            this._stream.write({
              serviceHandlerStatus: {
                eventName,
                serviceName: msg.invokeServiceHandler.service.name,
                status: 'completed',
                message: '',
              },
            });
          } catch (err) {
            this._stream.write({
              serviceHandlerStatus: {
                eventName,
                serviceName: msg.invokeServiceHandler.service.name,
                status: 'failed',
                message: err.message,
              },
            });
          }
        }
      }
    });

    this._stream.on('error', (err) => {
      console.error('[EventManager] Stream error:', err.message);
    });

    this._stream.on('end', () => {
      console.log('[EventManager] Stream ended by server.');
    });
  }

  async receive() {
    await this._ensureStream();

    // MOCK: send preprovision event
    setTimeout(() => {
      console.log('[EventManager] Mock sending preprovision event...');
      this._stream.emit('data', {
        invokeProjectHandler: {
          eventName: 'preprovision',
          project: {
            name: 'simple-template'
          }
        }
      });
    }, 2000);
  }

  async addProjectEventHandler(eventName, handler) {
    await this._ensureStream();
    this._projectHandlers[eventName] = handler;

    this._stream.write({
      subscribeProjectEvent: {
        eventNames: [eventName],
      },
    });

    console.log(`[EventManager] Subscribed to project event: ${eventName}`);
  }

  async addServiceEventHandler(eventName, handler, options = {}) {
    await this._ensureStream();
    this._serviceHandlers[eventName] = handler;

    this._stream.write({
      subscribeServiceEvent: {
        eventNames: [eventName],
        host: options.host || '',
        language: options.language || '',
      },
    });

    console.log(`[EventManager] Subscribed to service event: ${eventName}`);
  }

  removeProjectEventHandler(eventName) {
    delete this._projectHandlers[eventName];
  }

  removeServiceEventHandler(eventName) {
    delete this._serviceHandlers[eventName];
  }
}

module.exports = { EventManager };
