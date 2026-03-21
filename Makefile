.PHONY: all build build-frontend run test clean docker-build docker-push docker-up docker-down docker-logs

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION)"
BINARY := diginode-cc

all: build-frontend build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/diginode-cc

build-frontend:
	cd web && npm run build

run: build
	./$(BINARY)

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf web/dist

# Docker targets
docker-build:
	docker build -f docker/Dockerfile -t karamble/diginode-cc:latest .

docker-push: docker-build
	docker push karamble/diginode-cc:latest

docker-prod-build:
	docker buildx build --platform linux/arm64 -f docker/Dockerfile -t karamble/diginode-cc:latest .

docker-prod-push:
	docker buildx build --platform linux/arm64 -f docker/Dockerfile -t karamble/diginode-cc:latest --push .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f
