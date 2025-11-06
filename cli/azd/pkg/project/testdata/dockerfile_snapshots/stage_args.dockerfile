FROM node:18-alpine
ARG BUILD_DATE
ARG VERSION=1.0.0
WORKDIR /app
RUN echo "Build date: ${BUILD_DATE}"
RUN echo "Version: ${VERSION}"
COPY . .
CMD ["node", "server.js"]
