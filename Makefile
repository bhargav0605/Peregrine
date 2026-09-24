# Peregrine — project tasks. Each component is a separate build, so its targets
# cd into that directory.

BROKER_DIR := broker
CLIENT_DIR := client
BIN_DIR    := bin
BROKER_BIN := $(BIN_DIR)/broker
CLIENT_JAR := $(BIN_DIR)/client.jar

.DEFAULT_GOAL := help

.PHONY: help
help:
	@echo "Peregrine"
	@echo
	@echo "  make build          build both components"
	@echo "  make test           test both components"
	@echo "  make check          gate: formatting, vet and tests"
	@echo "  make clean          remove build output"
	@echo
	@echo "  make build-broker   build the Go broker into $(BROKER_BIN)"
	@echo "  make build-client   build the Java client into $(CLIENT_JAR)"
	@echo "  make run-broker     build and run the broker"
	@echo "  make run-client     build and run the client"
	@echo "  make test-broker    run Go tests"
	@echo "  make test-client    run Java tests"
	@echo
	@echo "  make fmt            format Go sources"
	@echo "  make vet            run go vet"

.PHONY: build
build: build-broker build-client

.PHONY: build-broker
build-broker:
	@mkdir -p $(BIN_DIR)
	cd $(BROKER_DIR) && go build -o ../$(BROKER_BIN) .
	@echo "built $(BROKER_BIN)"

.PHONY: build-client
build-client:
	@mkdir -p $(BIN_DIR)
	cd $(CLIENT_DIR) && ./mvnw -q -B package
	cp $(CLIENT_DIR)/target/client.jar $(CLIENT_JAR)
	@echo "built $(CLIENT_JAR)"

.PHONY: run-broker
run-broker: build-broker
	./$(BROKER_BIN) -config $(BROKER_DIR)/broker.cfg

.PHONY: run-client
run-client: build-client
	java -jar $(CLIENT_JAR) --config $(CLIENT_DIR)/client.cfg

.PHONY: test
test: test-broker test-client

.PHONY: test-broker
test-broker:
	cd $(BROKER_DIR) && go test -race ./...

.PHONY: test-client
test-client:
	cd $(CLIENT_DIR) && ./mvnw -B test

.PHONY: fmt
fmt:
	cd $(BROKER_DIR) && gofmt -w .

.PHONY: vet
vet:
	cd $(BROKER_DIR) && go vet ./...

# Reports problems without fixing them, so it is safe to run in CI.
.PHONY: check
check: vet test
	@cd $(BROKER_DIR) && unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: these files need formatting (run 'make fmt'):"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi
	@echo "check passed"

.PHONY: clean
clean:
	rm -rf $(BIN_DIR)
	cd $(CLIENT_DIR) && ./mvnw -q -B clean
	@echo "removed $(BIN_DIR) and client build output"
