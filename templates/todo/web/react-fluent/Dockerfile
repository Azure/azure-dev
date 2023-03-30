FROM node:16.13-alpine AS build

# make the 'app' folder the current working directory
WORKDIR /app

COPY . .

# install project dependencies
RUN npm ci
RUN npm run package

FROM nginx:alpine

WORKDIR /usr/share/nginx/html
COPY --from=build /app/build .
COPY --from=build /app/nginx/nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["/bin/sh", "-c", "nginx -g \"daemon off;\""]