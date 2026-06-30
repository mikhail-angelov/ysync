include .env
export

APP=ysync

.PHONY: run build test clean dry-run

run: ## Run service with watcher and local UI
	go run . -local "$(LOCAL_FOLDER)" -remote "$(REMOTE_FOLDER)" $(ARGS)

dry-run: ## Dry-run: print sync plan without mutation
	go run . -local "$(LOCAL_FOLDER)" -remote "$(REMOTE_FOLDER)" --dry-run --verbose $(ARGS)

build: ## Build binary
	go build -o $(APP) .

test: ## Run all tests
	go test ./ysync/... -v -count=1

clean: ## Clean build artifacts
	go clean
	rm -f $(APP)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
