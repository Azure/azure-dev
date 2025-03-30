# Stage 1: Build
FROM node:16-alpine AS builder
WORKDIR /app
# Copy package files and install dependencies
COPY package*.json ./
RUN npm install
# Copy all source files
COPY . .
# Build the Vite app (adjust the script name if needed)
RUN npm run build

# Stage 2: Serve with Nginx
FROM nginx:stable-alpine
# Remove default nginx configuration (if needed)
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
