const { app } = require("@azure/functions");

app.http("hello", {
    methods: ["GET"],
    authLevel: "anonymous",
    route: "hello",
    handler: async (request, context) => {
        context.log(`Request received from ${request.url}`);

        return {
            body: "Hello, `azd`."
        };
    }
});
