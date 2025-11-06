FROM golang:1.21-alpine
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o myapp
CMD ["./myapp"]
