// Intentionally distinct from ../web/index.js: this service MUST self-identify as "api" in the response body.
// See the sibling web/index.js comment for why.
const http = require('http');

const server = http.createServer((req, res) => {
    res.statusCode = 200;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ service: 'api' }));
});

server.listen(8080, () => {
    console.log('api service listening on 8080');
});
