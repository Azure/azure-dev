FROM node:18-alpine
# Install dependencies
WORKDIR /app
COPY package*.json ./
RUN npm install

# Copy application code
COPY . .

# This is a multi-line comment
# It spans multiple lines
# And provides detailed information
EXPOSE 3000
CMD ["node", "server.js"]
