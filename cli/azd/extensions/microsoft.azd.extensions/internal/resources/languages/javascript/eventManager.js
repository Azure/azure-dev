const AzdClient = require('./azdClient');

class EventManager {
  constructor(client) {
    this._client = client;
    this._stream = null;
    this._projectHandlers = {};
    this._serviceHandlers = {};
  }

  async _ensureStream() {
    if (!this._stream) {
      this._stream = this._client.Events.eventStream();

      // Send ready event when the stream is ready
      await new Promise((resolve) => {
        this._stream.write({ extensionReadyEvent: { status: 'ready' } });
        resolve();
      });

      this._listen();
    }
  }

  _listen() {
    this._stream.on('data', async (msg) => {
      if (msg.invokeProjectHandler) {
        const eventName = msg.invokeProjectHandler.eventName;
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
      console.error('[EventManager] Error:', err.message);
    });

    this._stream.on('end', () => {
      console.log('[EventManager] Stream ended.');
    });
  }

  async receive() {
    await this._ensureStream();
  }

  async addProjectEventHandler(eventName, handler) {
    await this._ensureStream();
    this._projectHandlers[eventName] = handler;

    this._stream.write({
      subscribeProjectEvent: {
        eventNames: [eventName],
      },
    });
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
  }

  removeProjectEventHandler(eventName) {
    delete this._projectHandlers[eventName];
  }

  removeServiceEventHandler(eventName) {
    delete this._serviceHandlers[eventName];
  }
}

module.exports = { EventManager };
