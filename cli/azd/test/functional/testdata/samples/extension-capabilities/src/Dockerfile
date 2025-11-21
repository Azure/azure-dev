# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

FROM node:18-alpine

WORKDIR /app

# Create a simple hello world application
RUN echo 'const http = require("http");' > server.js && \
    echo 'const server = http.createServer((req, res) => {' >> server.js && \
    echo '  res.writeHead(200, {"Content-Type": "text/plain"});' >> server.js && \
    echo '  res.end("Hello World from Docker!\\n");' >> server.js && \
    echo '});' >> server.js && \
    echo 'const port = process.env.PORT || 3000;' >> server.js && \
    echo 'server.listen(port, () => {' >> server.js && \
    echo '  console.log(`Server running on port ${port}`);' >> server.js && \
    echo '});' >> server.js

EXPOSE 3000

CMD ["node", "server.js"]