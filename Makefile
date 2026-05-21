BINARY=agent-runner

build:
	go build -o $(BINARY) ./cmd/agent-runner/

clean:
	rm -f $(BINARY)
	rm -rf ~/.local/share/web3-avatar-agent-runner

doctor: build
	./$(BINARY) doctor

run: build frontend-build
	@pkill -f 'agent-runner start' 2>/dev/null; sleep 1
	@if command -v portless >/dev/null 2>&1; then \
		REVIEW_BOTS="chatgpt-codex-connector[bot]" portless agent-runner ./$(BINARY) start; \
	else \
		REVIEW_BOTS="chatgpt-codex-connector[bot]" ./$(BINARY) start; \
	fi

run-dev: build
	@pkill -f 'agent-runner start' 2>/dev/null; sleep 1
	REVIEW_BOTS="chatgpt-codex-connector[bot]" env -u PORT -u HOST -u PORTLESS_URL ./$(BINARY) start &
	sleep 2
	cd frontend && npm run dev
	@pkill -f 'agent-runner start' 2>/dev/null

frontend-build:
	cd frontend && NODE_ENV=production npm run build

frontend-dev:
	cd frontend && npm run dev

status: build
	./$(BINARY) status

jobs: build
	./$(BINARY) jobs

plist-install: build
	./$(BINARY) plist install

plist-uninstall:
	./$(BINARY) plist uninstall

.PHONY: build clean doctor run run-dev status jobs plist-install plist-uninstall frontend-build frontend-dev
