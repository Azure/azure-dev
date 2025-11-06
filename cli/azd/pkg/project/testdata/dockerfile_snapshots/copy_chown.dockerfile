FROM node:18-alpine
WORKDIR /app
COPY --chown=node:node package*.json ./
RUN npm install
COPY --chown=node:node . .
USER node
CMD ["node", "server.js"]
