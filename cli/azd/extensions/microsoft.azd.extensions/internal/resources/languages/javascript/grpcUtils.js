const logger = require('./logger');

/**
 * Helper function to execute a unary GRPC call with a promise interface
 * @param {Object} client - The GRPC client object 
 * @param {String} methodName - The method name to execute
 * @param {Object} request - The request body
 * @param {Object} metadata - gRPC metadata to include
 * @returns {Promise<Object>} - The response
 */
function unary(client, methodName, request, metadata) {
  return new Promise((resolve, reject) => {
    logger.debug(`Calling gRPC method: ${methodName}`, { requestData: request });
    
    client[methodName](request, metadata, (err, response) => {
      if (err) {
        logger.error(`gRPC error in ${methodName}:`, { 
          error: err.message, 
          code: err.code,
          details: err.details
        });
        reject(err);
        return;
      }
      
      logger.debug(`gRPC ${methodName} successful`, {
        responseFields: Object.keys(response || {}).join(', ')
      });
      resolve(response);
    });
  });
}

module.exports = { unary };
