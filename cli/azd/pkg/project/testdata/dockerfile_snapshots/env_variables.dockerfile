FROM node:18-alpine
ENV NODE_ENV=production
ENV APP_DIR=/app
ENV PATH_WITH_SPACES="path with spaces"
ENV QUOTED="value\"with\"quotes"
WORKDIR $APP_DIR
COPY . .
CMD ["node", "server.js"]
