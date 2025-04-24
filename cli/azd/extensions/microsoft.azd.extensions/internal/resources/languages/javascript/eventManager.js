export class EventManager {
    constructor(client) {
      this.client = client;
      this.projectHandlers = {};
      this.serviceHandlers = {};
    }
  
    async receive(cancellationToken) {
      const stream = this.client.Events.eventStream();
  
      stream.write({ extensionReadyEvent: { status: 'ready' } });
  
      stream.on('data', async (msg) => {
        if (msg.invokeProjectHandler) {
          const handler = this.projectHandlers[msg.invokeProjectHandler.eventName];
          if (handler) await handler({ project: msg.invokeProjectHandler.project });
        } else if (msg.invokeServiceHandler) {
          const handler = this.serviceHandlers[msg.invokeServiceHandler.eventName];
          if (handler) await handler({
            project: msg.invokeServiceHandler.project,
            service: msg.invokeServiceHandler.service
          });
        }
      });
  
      stream.on('error', (err) => console.error('[EventManager] Error:', err));
      stream.on('end', () => console.log('[EventManager] Stream closed.'));
    }
  
    async addProjectEventHandler(name, handler) {
      this.projectHandlers[name] = handler;
    }
  
    async addServiceEventHandler(name, handler) {
      this.serviceHandlers[name] = handler;
    }
  }
  