FROM node:16.13-alpine AS build

# make the 'app' folder the current working directory
WORKDIR /app

COPY . .

# Fix up entrypoint line endings and make exec
RUN apk update && apk add --no-cache dos2unix \
    && dos2unix /app/entrypoint.sh && chmod +x /app/entrypoint.sh \
    && apk del dos2unix

# install project dependencies
RUN npm ci
RUN npm run build

FROM nginx:alpine

WORKDIR /usr/share/nginx/html
COPY --from=build /app/entrypoint.sh /bin
COPY --from=build /app/build .
COPY --from=build /app/nginx/nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["/bin/sh", "-c", "/bin/entrypoint.sh -o /usr/share/nginx/html/env-config.js && nginx -g \"daemon off;\""]