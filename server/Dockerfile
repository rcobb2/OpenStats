# Stage 1: Build the React/Vite frontend
FROM node:20-alpine AS web-builder
WORKDIR /app
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

# Stage 2: Build the Go backend
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /openlabstats-server ./cmd/server/

# Stage 3: Final lightweight image
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata

COPY --from=go-builder /openlabstats-server /usr/local/bin/openlabstats-server
COPY --from=web-builder /app/dist /usr/local/bin/public
COPY config/server.yaml /etc/openlabstats/server.yaml

WORKDIR /usr/local/bin
EXPOSE 8080

ENTRYPOINT ["openlabstats-server", "/etc/openlabstats/server.yaml"]
