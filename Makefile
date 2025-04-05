.PHONY: build run-stdio run-sse clean test client client-simple test-script build-example docker-build docker-run docker-run-stdio docker-stop

# Build the server
build:
	go build -o bin/server cmd/server/main.go
	GOOS=linux GOARCH=amd64 go build -o bin/server-linux cmd/server/main.go

# Build the example stdio server
build-example:
	cd examples && go build -o mcp-example mcp_stdio_example.go

# Run the server in stdio mode
run-stdio: build
	./server -t stdio

# Run the server in SSE mode
run-sse: clean build
	./server -t sse -p 9090 -h 127.0.0.1 -c /Users/harvey/Work/dev/FreePeak/SaaS/cashflow-core/database_config.json

# Build and run the example client
client:
	go build -o mcp-client examples/client/client.go
	./mcp-client

# Build and run the simple client (no SSE dependency)
client-simple:
	go build -o mcp-simple-client examples/client/simple_client.go
	./mcp-simple-client

# Run the test script
test-script:
	./examples/test_script.sh

# Run tests
test:
	go test ./... -race -cover -count=1

# Clean build artifacts
clean:
	rm -f server server-linux mcp-client mcp-simple-client
	# lsof -i :9090 | grep LISTEN | awk '{print $2}' | xargs kill -9

# Run linter
lint:
	golangci-lint run ./...

# Setup
setup:
	go mod tidy
	go mod download

# Docker targets
docker-build:
	docker build -t db-mcp-server:latest .

# Run the Docker container in SSE mode
docker-run:
	docker run -d --name db-mcp-server -p 9092:9092 -v $(PWD)/config.json:/app/config.json -v $(PWD)/logs:/app/logs db-mcp-server:latest

# Run Docker container in STDIO mode (for debugging)
docker-run-stdio:
	docker run -it --rm --name db-mcp-server-stdio -v $(PWD)/config.json:/app/config.json -e TRANSPORT_MODE=stdio db-mcp-server:latest

# Stop and remove the Docker container
docker-stop:
	docker stop db-mcp-server || true
	docker rm db-mcp-server || true

deploy-docker:
	docker buildx build -t freepeak/db-mcp-server:latest .
	docker push freepeak/db-mcp-server:latest

# Default target
all: build 