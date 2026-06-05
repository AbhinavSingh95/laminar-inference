.PHONY: help build test smoke clean run benchmark worker gateway proto format

# Default target
help:
	@echo "Laminar Inference - Make Commands"
	@echo ""
	@echo "Available targets:"
	@echo "  make build      - Build all targets (worker + gateway)"
	@echo "  make worker     - Build C++ worker only"
	@echo "  make gateway    - Build Go gateway only"
	@echo "  make proto      - Build proto definitions"
	@echo "  make run        - Start both services"
	@echo "  make test       - Run Bazel tests"
	@echo "  make smoke      - Run live HTTP/gRPC smoke test"
	@echo "  make benchmark  - Run performance benchmarks"
	@echo "  make clean      - Clean all build artifacts"
	@echo "  make format     - Format Go code"
	@echo "  make help       - Show this help message"

# Build all targets
build:
	@echo "Building all targets..."
	bazel build //...

# Build individual targets
worker:
	@echo "Building C++ worker..."
	bazel build //backend:worker

gateway:
	@echo "Building Go gateway..."
	bazel build //gateway:gateway

proto:
	@echo "Building proto definitions..."
	bazel build //proto:inference_go_proto //proto:inference_cc_grpc

# Run the system
run: build
	@echo "Starting Laminar Inference..."
	./run.sh

# Run tests
test:
	@echo "Running tests..."
	bazel test //...

# Run live smoke test
smoke:
	@echo "Running smoke test..."
	./test.sh

# Run benchmarks
benchmark:
	@echo "Running benchmarks..."
	./benchmark.sh

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	bazel clean

# Deep clean
clean-all:
	@echo "Deep cleaning all artifacts..."
	bazel clean --expunge
	rm -rf bazel-*

# Format Go code
format:
	@echo "Formatting Go code..."
	bazel run //:gazelle
	gofmt -w gateway/

# Show project structure
tree:
	@tree -I 'bazel-*' -L 2

# Quick health check
check:
	@echo "Checking if services are running..."
	@curl -s http://localhost:8080/health || echo "Gateway is not running"
	@echo ""

# Show metrics
metrics:
	@echo "Fetching metrics..."
	@curl -s http://localhost:8080/metrics | grep -E "(batch_size|request_duration)" | grep -v "^#"
