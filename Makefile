BINARY=agent-runner

build:
	go build -o $(BINARY) ./cmd/agent-runner/

clean:
	rm -f $(BINARY)
	rm -rf ~/.local/share/web3-avatar-agent-runner

doctor: build
	./$(BINARY) doctor

run: build
	@if command -v portless >/dev/null 2>&1; then \
		portless agent-runner ./$(BINARY) start; \
	else \
		./$(BINARY) start; \
	fi

status: build
	./$(BINARY) status

jobs: build
	./$(BINARY) jobs

plist-install: build
	./$(BINARY) plist install

plist-uninstall:
	./$(BINARY) plist uninstall

.PHONY: build clean doctor run status jobs plist-install plist-uninstall
