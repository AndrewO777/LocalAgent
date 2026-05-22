# LocalAgent build orchestration.
#
# This drives the two halves of the project (Go binary + Vite/React UI). Each
# target is intentionally a thin wrapper around the underlying npm/go command
# so Windows users without Make can read it and run the equivalent by hand.

BINARY := LocalAgent
ifeq ($(OS),Windows_NT)
  BINARY := LocalAgent.exe
endif

WEB_DIR := web

.PHONY: help
help:
	@echo "LocalAgent — common targets"
	@echo ""
	@echo "  make deps       — install UI dependencies (npm install)"
	@echo "  make build      — npm run build, then go build (produces $(BINARY))"
	@echo "  make ui         — npm run build only (refresh embedded assets)"
	@echo "  make go         — go build only (uses whatever's in web/dist)"
	@echo "  make dev        — vite + go run side-by-side (see notes in README)"
	@echo "  make test       — go test ./... and tsc --noEmit"
	@echo "  make clean      — remove the binary and dist/ artifacts"
	@echo ""

.PHONY: deps
deps:
	cd $(WEB_DIR) && npm install

.PHONY: ui
ui:
	cd $(WEB_DIR) && npm run build

.PHONY: go
go:
	go build -o $(BINARY) .

.PHONY: build
build: ui go

.PHONY: test
test:
	go test ./...
	cd $(WEB_DIR) && npm run typecheck

# `make dev` runs the Go server on :8080 and the Vite dev server on :5173
# side-by-side. The Vite proxy forwards /api requests (including the SSE
# stream) to :8080, so the UI gets HMR and the agent loop runs at full
# fidelity. Ctrl+C kills both, regardless of which one died first.
#
# The real work happens in scripts/devloop — a small cross-platform Go
# runner. We could have done this with shell tricks (trap + backgrounding)
# but those don't survive Windows / cmd.exe, and the Go version is the
# same on every OS.
.PHONY: dev
dev:
	go run ./scripts/devloop

.PHONY: clean
clean:
	rm -f $(BINARY) LocalAgent.exe LocalAgent
	rm -rf $(WEB_DIR)/dist/assets $(WEB_DIR)/dist/*.js $(WEB_DIR)/dist/*.css
	@echo "Cleaned binary and dist artifacts. The placeholder dist/index.html is kept."
