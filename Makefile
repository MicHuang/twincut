.PHONY: help build test test-script test-go test-smoke install uninstall clean

GOFLAGS ?= -trimpath -ldflags="-s -w"

help:
	@echo "twincut — make targets"
	@echo ""
	@echo "  build           build bin/twincut-ui (Go binary)"
	@echo "  test            run all tests (script + Go + smoke)"
	@echo "  test-script     run twincut.sh --json-events test suite"
	@echo "  test-go         run Go unit tests"
	@echo "  test-smoke      run shell smoke suites"
	@echo "  install         symlink scripts into ~/.local/bin"
	@echo "  uninstall       remove the symlinks"
	@echo "  clean           remove built binaries"

build:
	@cd ui && go build $(GOFLAGS) -o ../bin/twincut-ui .
	@echo "built bin/twincut-ui ($$(du -h bin/twincut-ui | awk '{print $$1}'))"

test: test-script test-go test-smoke

test-script:
	@python3 tests/json_events/run_tests.py

test-go:
	@cd ui && go test ./...

test-smoke:
	@bash tests/events_contract.sh
	@bash tests/legacy_event_ts_seam.sh
	@bash tests/p0_smoke.sh
	@bash tests/p1_stage9_smoke.sh
	@bash tests/p1_stage11_smoke.sh
	@bash tests/vid_eq_smoke.sh
	@bash tests/backup_selfcheck_smoke.sh

install: build
	@installers/install.sh

uninstall:
	@installers/uninstall.sh

clean:
	@rm -f bin/twincut-ui
