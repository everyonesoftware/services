# Build the React dashboard.
FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Compile the Go API and dashboard server.
FROM golang:1.24-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG APP_VERSION=0
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.appVersion=${APP_VERSION}" -o /out/services ./cmd/services

# Minimal runtime image.
FROM alpine:3.22
LABEL io.services.worker.protocol="1"
LABEL io.services.storage.bind="1"
RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose && docker compose version && addgroup -S services && adduser -S -G services services && mkdir -p /data && chown services:services /data && chmod 700 /data
WORKDIR /app
COPY --from=go-build /out/services /app/services
COPY --from=web-build /src/web/dist /app/web/dist
ENV LISTEN_ADDR=:8080
ENV SERVICES_DATA_DIR=/data
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=6 CMD wget -q -O /dev/null http://127.0.0.1:8080/api/ready || exit 1
USER services
ENTRYPOINT ["/app/services"]
