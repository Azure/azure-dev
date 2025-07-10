# GitHub Issues Analysis Tool Makefile

.PHONY: all build clean test lint run help

# Default target
all: build

# Build the tool
build:
	go build -o issues-analysis .

# Clean build artifacts
clean:
	rm -f issues-analysis
	rm -f *.json
	rm -f *.md
	rm -f *.out

# Run tests
test:
	go test ./...

# Lint the code
lint:
	gofmt -s -w .
	go vet ./...

# Run the tool with default settings
run: build
	./issues-analysis analyze

# Run the tool and generate markdown report
report: build
	./issues-analysis analyze --output markdown > issues-analysis-report.md
	@echo "Report generated: issues-analysis-report.md"

# Run the tool and generate JSON report
json: build
	./issues-analysis analyze --output json > issues-analysis-report.json
	@echo "JSON report generated: issues-analysis-report.json"

# Show help
help:
	@echo "Available targets:"
	@echo "  all     - Build the tool (default)"
	@echo "  build   - Build the tool"
	@echo "  clean   - Clean build artifacts"
	@echo "  test    - Run tests"
	@echo "  lint    - Lint the code"
	@echo "  run     - Build and run with default settings"
	@echo "  report  - Generate markdown report"
	@echo "  json    - Generate JSON report"
	@echo "  help    - Show this help message"