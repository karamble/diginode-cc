## Build stage - Go backend
FROM golang:1.25-alpine AS backend-builder

RUN apk add --no-cache git

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o /diginode-cc ./cmd/diginode-cc

## Build stage - React frontend
FROM node:20-alpine AS frontend-builder

WORKDIR /build
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ .
RUN npm run build

## Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=backend-builder /diginode-cc /app/diginode-cc
COPY --from=frontend-builder /build/dist /app/web/dist

EXPOSE 3000

ENTRYPOINT ["/app/diginode-cc"]
