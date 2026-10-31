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
COPY go.mod ./
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG APP_VERSION=0
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.appVersion=${APP_VERSION}" -o /out/services ./cmd/services

# Minimal runtime image.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S services && adduser -S -G services services
WORKDIR /app
COPY --from=go-build /out/services /app/services
COPY --from=web-build /src/web/dist /app/web/dist
ENV LISTEN_ADDR=:8080
EXPOSE 8080
USER services
ENTRYPOINT ["/app/services"]
