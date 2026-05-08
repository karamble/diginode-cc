.PHONY: all build build-frontend run test clean proto proto-tools docker-build docker-push docker-up docker-down docker-logs

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION)"
BINARY := diginode-cc

# Meshtastic protobuf generation.
# The protos are NOT vendored and the generated bindings are NOT committed
# (internal/meshpb/ is gitignored). `make proto` downloads the source tarball
# at the pinned SHA, runs protoc against it in a temp dir, and writes the
# generated Go bindings to internal/meshpb/. `make build` and `make all`
# auto-trigger generation if internal/meshpb/ is missing, so first-time
# clones just run `make all` -- they do still need protoc + protoc-gen-go
# + curl on PATH (see proto-tools to install protoc-gen-go).
#
# To bump the pin: change PROTO_SHA below, run `make proto`, test, commit
# the Makefile change.
PROTO_REPO   := meshtastic/protobufs
PROTO_SHA    := 97ea65a10d31f24d84c8510342f2cd2d213c35a5
PROTO_OUT    := internal/meshpb
PROTO_MODULE := github.com/karamble/diginode-cc
PROTO_FILES := \
	meshtastic/admin.proto \
	meshtastic/atak.proto \
	meshtastic/channel.proto \
	meshtastic/config.proto \
	meshtastic/connection_status.proto \
	meshtastic/device_ui.proto \
	meshtastic/deviceonly.proto \
	meshtastic/localonly.proto \
	meshtastic/mesh.proto \
	meshtastic/module_config.proto \
	meshtastic/portnums.proto \
	meshtastic/remote_hardware.proto \
	meshtastic/telemetry.proto \
	meshtastic/xmodem.proto

# Remap each .proto's go_package option to our module path so generated files
# land flat in $(PROTO_OUT)/ under package "meshpb", regardless of what the
# upstream protos declare ("github.com/meshtastic/go/generated"). The
# `path;name` form forces both the import path and the Go package name.
PROTO_M_FLAGS := $(foreach f,$(PROTO_FILES),--go_opt=M$f=$(PROTO_MODULE)/$(PROTO_OUT)\;meshpb)

all: build-frontend build

# Auto-trigger proto generation if the generated package is missing.
# Order-only prerequisite (the | guard) so it doesn't re-run on every build
# once the directory exists.
build: | $(PROTO_OUT)
	go build $(LDFLAGS) -o $(BINARY) ./cmd/diginode-cc

# Stamp target: presence of $(PROTO_OUT) is enough to satisfy the prereq.
# Run `make proto` to refresh after bumping PROTO_SHA.
$(PROTO_OUT):
	@echo "internal/meshpb/ missing -- running make proto"
	@$(MAKE) proto

build-frontend:
	cd web && npm run build

run: build
	./$(BINARY)

test: | $(PROTO_OUT)
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
		rm -rf $(PROTO_OUT) && mkdir -p $(PROTO_OUT) && \
		protoc \
			--experimental_allow_proto3_optional \
			--proto_path=$$TMPDIR \
			--go_out=. \
			--go_opt=module=$(PROTO_MODULE) \
			$(PROTO_M_FLAGS) \
			$(addprefix $$TMPDIR/,$(PROTO_FILES)) && \
		sed -i '/^\t_ "github\.com\/meshtastic\/go\/generated"$$/d' $(PROTO_OUT)/*.pb.go && \
		echo "Generated bindings in $(PROTO_OUT)/ from $(PROTO_REPO)@$(PROTO_SHA)"

clean:
	rm -f $(BINARY)
	rm -rf web/dist
	rm -rf $(PROTO_OUT)

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
