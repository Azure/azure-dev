FROM golang:1.26.1-alpine AS build
WORKDIR /app
COPY . .
RUN go build -o /job .

FROM alpine:3.19
COPY --from=build /job /job
CMD ["/job"]
