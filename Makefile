.PHONY: help build test test-script test-go install uninstall clean

GOFLAGS ?= -trimpath -ldflags="-s -w"

help:
	@echo "twincut — make targets"
	@echo ""
	@echo "  build           build bin/twincut-ui (Go binary)"
	@echo "  test            run all tests (script + Go)"
	@echo "  test-script     run twincut.sh --json-events test suite"
	@echo "  test-go         run Go unit tests"
	@echo "  install         symlink scripts into ~/.local/bin"
	@echo "  uninstall       remove the symlinks"
	@echo "  clean           remove built binaries"

build:
	@cd ui && go build $(GOFLAGS) -o ../bin/twincut-ui .
	@echo "built bin/twincut-ui ($$(du -h bin/twincut-ui | awk '{print $$1}'))"

test: test-script test-go

test-script:
	@python3 tests/json_events/run_tests.py

test-go:
	@cd ui && go test ./...

install: build
	@installers/install.sh

uninstall:
	@installers/uninstall.sh

clean:
	@rm -f bin/twincut-ui
