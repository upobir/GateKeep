.PHONY: build frontend run

build: frontend
	go build -o bin/gatekeep ./cmd/gatekeep

frontend:
	@set -e; \
	if [ -s "$(HOME)/.nvm/nvm.sh" ]; then . "$(HOME)/.nvm/nvm.sh"; nvm use; fi; \
	npm --prefix web ci; \
	npm --prefix web run build

run: build
	./bin/gatekeep
