.PHONY: build frontend run install uninstall

APP = bin/GateKeep.app
BIN = $(APP)/Contents/MacOS/gatekeep
ICONSET = bin/AppIcon.iconset
INSTALLED = $(HOME)/Applications/GateKeep.app

build: frontend
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources $(ICONSET)
	go build -o $(BIN) ./cmd/gatekeep
	go run ./internal/icon/mkicon.go -o $(ICONSET)
	iconutil -c icns $(ICONSET) -o $(APP)/Contents/Resources/AppIcon.icns
	rm -rf $(ICONSET)
	cp internal/notify/Info.plist $(APP)/Contents/Info.plist
	rm -f bin/gatekeep
	codesign --force --sign - $(APP)
	/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f $(APP)

install: build
	mkdir -p "$(HOME)/Applications"
	rm -rf "$(INSTALLED)"
	ditto $(APP) "$(INSTALLED)"
	/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$(INSTALLED)"

uninstall:
	/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -u "$(INSTALLED)"
	rm -rf "$(INSTALLED)"

frontend:
	@set -e; \
	if [ -s "$(HOME)/.nvm/nvm.sh" ]; then . "$(HOME)/.nvm/nvm.sh"; nvm use; fi; \
	npm --prefix web ci; \
	npm --prefix web run build

run: build
	./$(BIN)
