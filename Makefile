BINARY    := syu
CMD_PATH  := ./cmd/syu
INSTALL   := /usr/local/bin
BUILD_DIR := build

.PHONY: all build install uninstall clean deps test

all: build

deps:
	go mod tidy
	go mod download

build: deps
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY) $(CMD_PATH)
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

install: build
	@echo "Installing $(BINARY) to $(INSTALL)/$(BINARY) ..."
	install -Dm755 $(BUILD_DIR)/$(BINARY) $(INSTALL)/$(BINARY)
	@echo "Done. Run: sudo syu"

uninstall:
	rm -f $(INSTALL)/$(BINARY)
	@echo "Removed $(INSTALL)/$(BINARY)"

clean:
	rm -rf $(BUILD_DIR)

# Quick test: list sessions (no root needed)
test:
	$(BUILD_DIR)/$(BINARY) list
