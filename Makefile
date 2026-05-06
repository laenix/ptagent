.PHONY: all build server dispatcher ptagent web clean dev dist

all: build

# Build Go binaries
build: server dispatcher ptagent

server:
	go build -o bin/ptagent-server ./cmd/server

dispatcher:
	go build -o bin/ptagent-dispatcher ./cmd/dispatcher

ptagent:
	go build -o bin/ptagent ./cmd/ptagent

# Development
dev-server:
	go run ./cmd/server --addr :8000

dev-dispatcher:
	go run ./cmd/dispatcher --config ./configs/dispatch.yaml

dev-web:
	cd web && npm run dev

# Install frontend deps
web-install:
	cd web && npm install

# Build frontend
web-build:
	cd web && npm run build

# Run tests
test:
	go test ./...

# Tidy
tidy:
	go mod tidy

# Clean
clean:
	rm -rf bin/
	rm -rf web/dist/
	rm -rf data/

# Docker
docker-build:
	docker build -t ptagent:latest .

docker-worker:
	docker build -t ptagent-worker:latest -f Dockerfile.worker .

docker-up: docker-worker
	docker compose up --build

docker-down:
	docker compose down

# 单文件部署包 (Linux amd64)
dist: web-build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/ptagent ./cmd/ptagent
	mkdir -p dist/configs dist/prompts dist/web
	cp -r configs/* dist/configs/
	cp -r prompts/* dist/prompts/
	cp -r web/dist dist/web/dist
	cp Dockerfile.worker dist/
	@echo "Deploy package ready in dist/"
	@echo "  scp -r dist/* user@server:/opt/ptagent/"
