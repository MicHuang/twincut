.PHONY: help test test-script install uninstall

help:
	@echo "twincut — make targets"
	@echo ""
	@echo "  test            run all tests"
	@echo "  test-script     run twincut.sh --json-events test suite"
	@echo "  install         symlink scripts into ~/.local/bin"
	@echo "  uninstall       remove the symlinks"

test: test-script

test-script:
	@python3 tests/json_events/run_tests.py

install:
	@installers/install.sh

uninstall:
	@installers/uninstall.sh
