.PHONY: all build build-frontend run test clean proto proto-tools proto-check docker-build docker-push docker-up docker-down docker-logs

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION)"
BINARY := diginode-cc

# Meshtastic protobuf generation.
# The protos are NOT vendored. `make proto` downloads the source tarball at
# the pinned SHA, runs protoc against it in a temp dir, and writes the
# generated Go bindings to internal/meshpb/. The generated files are checked
# in, so routine builds and CI don't need protoc or network access -- only
# someone bumping the pin (or running proto-check in CI) does.
#
# To bump: change PROTO_SHA below, run `make proto`, commit the diff in
# internal/meshpb/.
PROTO_REPO := meshtastic/protobufs
PROTO_SHA  := 97ea65a10d31f24d84c8510342f2cd2d213c35a5
PROTO_OUT  := internal/meshpb
PROTO_FILES := \
	meshtastic/admin.proto \
	meshtastic/channel.proto \
	meshtastic/config.proto \
	meshtastic/mesh.proto \
	meshtastic/portnums.proto \
	meshtastic/module_config.proto \
	meshtastic/telemetry.proto \
	meshtastic/deviceonly.proto \
	meshtastic/localonly.proto \
	meshtastic/connection_status.proto \
	meshtastic/xmodem.proto \
	meshtastic/remote_hardware.proto

all: build-frontend build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/diginode-cc

build-frontend:
	cd web && npm run build

run: build
	./$(BINARY)

test:
	go test ./...

# proto-tools installs the protoc-gen-go plugin pinned in tools/tools.go.
# Run once after cloning, or when bumping the plugin version.
proto-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go

# proto regenerates internal/meshpb/ from the pinned Meshtastic .proto files.
# Downloads github.com/$(PROTO_REPO)@$(PROTO_SHA) into a temp dir, runs protoc,
# then cleans up. Requires protoc on PATH and protoc-gen-go installed (see
# proto-tools). Network access required only for this target.
proto:
	@command -v protoc >/dev/null 2>&1 || { echo "ERROR: protoc not found on PATH"; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || { echo "ERROR: protoc-gen-go not found. Run: make proto-tools"; exit 1; }
	@command -v curl >/dev/null 2>&1 || { echo "ERROR: curl not found on PATH"; exit 1; }
	@TMPDIR=$$(mktemp -d) && trap "rm -rf $$TMPDIR" EXIT && \
		echo "Fetching $(PROTO_REPO)@$(PROTO_SHA)..." && \
		curl -fsSL "https://github.com/$(PROTO_REPO)/archive/$(PROTO_SHA).tar.gz" \
			| tar -xz -C $$TMPDIR --strip-components=1 && \
		mkdir -p $(PROTO_OUT) && \
		protoc \
			--proto_path=$$TMPDIR \
			--go_out=$(PROTO_OUT) \
			--go_opt=paths=source_relative \
			$(addprefix $$TMPDIR/,$(PROTO_FILES)) && \
		echo "Generated bindings in $(PROTO_OUT)/ from $(PROTO_REPO)@$(PROTO_SHA)"

# proto-check is the CI guard: regenerate, then assert the working tree is clean.
# A non-empty diff means someone hand-edited internal/meshpb/ or the pinned SHA
# was bumped without committing the regenerated files.
proto-check: proto
	@if ! git diff --quiet -- $(PROTO_OUT); then \
		echo "ERROR: $(PROTO_OUT) is out of sync with $(PROTO_REPO)@$(PROTO_SHA). Run 'make proto' and commit."; \
		git diff -- $(PROTO_OUT); \
		exit 1; \
	fi
	@echo "$(PROTO_OUT) is in sync with $(PROTO_REPO)@$(PROTO_SHA)"

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
