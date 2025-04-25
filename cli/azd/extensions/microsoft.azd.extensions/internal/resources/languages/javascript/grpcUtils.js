// grpcUtils.js
function unary(client, methodName, request, metadata) {
  return new Promise((resolve, reject) => {
    client[methodName](request, metadata, (err, response) => {
      if (err) reject(err);
      else resolve(response);
    });
  });
}

module.exports = { unary };
