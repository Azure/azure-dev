const logger = require('./logger');

/**
 * Helper function to execute a unary GRPC call with a promise interface
 * @param {Object} client - The GRPC client object 
 * @param {String} methodName - The method name to execute
 * @param {Object} request - The request body (must be a protobuf object)
 * @param {Object} metadata - gRPC metadata to include
 * @returns {Promise<Object>} - The response
 */
function unary(client, methodName, request, metadata) {
  return new Promise((resolve, reject) => {
    if (typeof request?.serializeBinary !== 'function') {
      const errMsg = `Invalid request object passed to gRPC method '${methodName}': Missing serializeBinary() method`;
      logger.error(errMsg, { actualType: typeof request });
      return reject(new Error(errMsg));
    }

    logger.debug(`Calling gRPC method: ${methodName}`, {
      requestType: request?.constructor?.name
    });

    client[methodName](request, metadata, (err, response) => {
      if (err) {
        logger.error(`gRPC error in ${methodName}:`, { 
          error: err.message, 
          code: err.code,
          details: err.details
        });
        return reject(err);
      }

      logger.debug(`gRPC ${methodName} successful`, {
        responseFields: Object.keys(response || {}).join(', ')
      });
      resolve(response);
    });
  });
}

module.exports = { unary };
