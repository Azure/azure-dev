// Intentionally distinct from ../api/index.js: this service MUST self-identify as "web" in the response body.
// The multi-service remote-build functional test asserts each service serves its own identity, which catches the
// ACR correlation-id blob-path collision bug where parallel uploads clobber each other and one service ends up
// running the other service's image.
const http = require('http');

const server = http.createServer((req, res) => {
    res.statusCode = 200;
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ service: 'web' }));
});

server.listen(8080, () => {
    console.log('web service listening on 8080');
});
