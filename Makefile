-include .env
export

APP=ysync
BIN ?= bin/$(APP)
REMOTE_FOLDER ?= /
URL ?= https://webdav.yandex.ru
CONFLICT ?= skip
DELETE_MODE ?= trash
KEYCHAIN_PROFILE ?=
INSTALL_KEYCHAIN_PROFILE = $(if $(KEYCHAIN_PROFILE),$(KEYCHAIN_PROFILE),ysync)
INSTALL_DIR ?= $(HOME)/.local/bin
LAUNCH_AGENT_LABEL ?= com.ysync.service
LAUNCH_AGENT_PLIST ?= $(HOME)/Library/LaunchAgents/$(LAUNCH_AGENT_LABEL).plist
LOG_DIR ?= $(HOME)/Library/Logs/ysync
ifneq ($(strip $(KEYCHAIN_PROFILE)),)
YSYNC_AUTH_ARGS = -keychain-profile "$(KEYCHAIN_PROFILE)"
endif
YSYNC_ARGS = -local "$(LOCAL_FOLDER)" -remote "$(REMOTE_FOLDER)" -url "$(URL)" -conflict "$(CONFLICT)" -delete-mode "$(DELETE_MODE)" $(YSYNC_AUTH_ARGS) $(ARGS)
ifneq ($(strip $(YANDEX_USER)$(YANDEX_PASS)$(YANDEX_TOKEN)),)
ifeq ($(strip $(KEYCHAIN_PROFILE)),)
INSTALL_AUTH_ARGS = -keychain-profile "$(INSTALL_KEYCHAIN_PROFILE)"
endif
endif

.PHONY: run build test clean dry-run install-macos

run: ## Run service with watcher and local UI
	go run . $(YSYNC_ARGS)

dry-run: ## Dry-run: print sync plan without mutation
	go run . $(YSYNC_ARGS) --dry-run --verbose

build: ## Build binary
	mkdir -p "$(dir $(BIN))"
	go build -o "$(BIN)" .

install-macos: build ## Install binary and LaunchAgent for the current macOS user
	@test -n "$(LOCAL_FOLDER)" || (echo "LOCAL_FOLDER is required, e.g. make install-macos LOCAL_FOLDER=$$HOME/SyncFolder REMOTE_FOLDER=/"; exit 1)
	mkdir -p "$(INSTALL_DIR)" "$(dir $(LAUNCH_AGENT_PLIST))" "$(LOG_DIR)"
	install -m 0755 "$(BIN)" "$(INSTALL_DIR)/$(APP)"
	@if [ -n "$$YANDEX_USER" ]; then security add-generic-password -U -s "$(INSTALL_KEYCHAIN_PROFILE)" -a yandex_user -w "$$YANDEX_USER" >/dev/null; fi
	@if [ -n "$$YANDEX_PASS" ]; then security add-generic-password -U -s "$(INSTALL_KEYCHAIN_PROFILE)" -a yandex_pass -w "$$YANDEX_PASS" >/dev/null; elif [ -n "$$YANDEX_TOKEN" ]; then security add-generic-password -U -s "$(INSTALL_KEYCHAIN_PROFILE)" -a yandex_pass -w "$$YANDEX_TOKEN" >/dev/null; fi
	launchctl bootout "gui/$$(id -u)/$(LAUNCH_AGENT_LABEL)" 2>/dev/null || true
	"$(INSTALL_DIR)/$(APP)" $(YSYNC_ARGS) $(INSTALL_AUTH_ARGS) --print-launch-agent > "$(LAUNCH_AGENT_PLIST)"
	launchctl bootstrap "gui/$$(id -u)" "$(LAUNCH_AGENT_PLIST)"
	launchctl enable "gui/$$(id -u)/$(LAUNCH_AGENT_LABEL)"
	launchctl kickstart -k "gui/$$(id -u)/$(LAUNCH_AGENT_LABEL)"

test: ## Run all tests
	go test ./ysync/... -v -count=1

clean: ## Clean build artifacts
	go clean
	rm -f "$(BIN)"

help: ## Show this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
