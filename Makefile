.PHONY: build app frontend run install uninstall dmg package release clean

# Local builds always carry a development label, even when HEAD is tagged.
BUILD_MODE ?= dev
ifeq ($(BUILD_MODE),release)
override VERSION := $(RELEASE_VERSION)
PLIST_VERSION = $(patsubst v%,%,$(VERSION))
else
override VERSION := $(shell bash scripts/version.sh dev)
PLIST_VERSION = 0.0.0
endif
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

# Target architecture: arm64 (Apple Silicon) or amd64 (Intel). Defaults to this Mac.
GOARCH ?= $(shell go env GOARCH)
CLANG_ARCH_arm64 = arm64
CLANG_ARCH_amd64 = x86_64
RELEASE_ARCHES = arm64 amd64

OUT = bin/darwin-$(GOARCH)
APP = $(OUT)/GateKeep.app
BIN = $(APP)/Contents/MacOS/gatekeep
ICONSET = $(OUT)/AppIcon.iconset
DMG = bin/GateKeep-$(VERSION)-macos-$(GOARCH).dmg
INSTALLED = $(HOME)/Applications/GateKeep.app
LSREGISTER = /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister

# Frontend + app for this Mac.
build: frontend app

# App bundle only; assumes the frontend is already built.
app:
	@bash scripts/version.sh "$(BUILD_MODE)" "$(RELEASE_VERSION)" >/dev/null
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources $(ICONSET)
	# Keep linker flags in Go build metadata so cross-architecture versions can be verified.
	CGO_ENABLED=1 GOOS=darwin GOARCH=$(GOARCH) CC="clang -arch $(CLANG_ARCH_$(GOARCH))" \
		go build -ldflags "-X gatekeep/internal/buildinfo.Version=$(VERSION) -X gatekeep/internal/buildinfo.Commit=$(COMMIT)" -o $(BIN) ./cmd/gatekeep
	@bash scripts/verify-version.sh "$(BIN)" "$(VERSION)" "$(COMMIT)"
	go run ./internal/icon/mkicon.go -o $(ICONSET)
	iconutil -c icns $(ICONSET) -o $(APP)/Contents/Resources/AppIcon.icns
	rm -rf $(ICONSET)
	cp internal/notify/Info.plist $(APP)/Contents/Info.plist
	plutil -replace CFBundleShortVersionString -string $(PLIST_VERSION) $(APP)/Contents/Info.plist
	plutil -replace CFBundleVersion -string $(PLIST_VERSION) $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)
	$(LSREGISTER) -f $(APP)

# DMG for one arch; assumes the frontend is already built.
package: app
	rm -rf $(OUT)/dmg-stage $(OUT)/GateKeep-rw.dmg "$(DMG)"
	mkdir -p $(OUT)/dmg-stage
	ditto $(APP) $(OUT)/dmg-stage/GateKeep.app
	ln -s /Applications $(OUT)/dmg-stage/Applications
	hdiutil create -volname GateKeep -srcfolder $(OUT)/dmg-stage -ov -format UDRW $(OUT)/GateKeep-rw.dmg
	rm -rf $(OUT)/dmg-stage
	bash scripts/layout-dmg.sh $(OUT)/GateKeep-rw.dmg
	hdiutil convert $(OUT)/GateKeep-rw.dmg -format UDZO -imagekey zlib-level=9 -o "$(DMG)"
	rm -f $(OUT)/GateKeep-rw.dmg

# DMG for this Mac.
dmg: frontend package

# DMGs for every release arch, plus a checksum file.
release:
	@bash scripts/version.sh release "$(RELEASE_VERSION)" >/dev/null
	$(MAKE) frontend
	@for arch in $(RELEASE_ARCHES); do $(MAKE) package GOARCH=$$arch BUILD_MODE=release RELEASE_VERSION="$(RELEASE_VERSION)" || exit 1; done
	cd bin && shasum -a 256 GateKeep-$(RELEASE_VERSION)-*.dmg > GateKeep-$(RELEASE_VERSION)-checksums.txt

frontend:
	@set -e; \
	if [ -z "$$CI" ] && [ -s "$(HOME)/.nvm/nvm.sh" ]; then . "$(HOME)/.nvm/nvm.sh"; nvm use; fi; \
	npm --prefix web ci; \
	npm --prefix web run build

run: build
	./$(BIN)

install: build
	mkdir -p "$(HOME)/Applications"
	rm -rf "$(INSTALLED)"
	ditto $(APP) "$(INSTALLED)"
	$(LSREGISTER) -f "$(INSTALLED)"

uninstall:
	$(LSREGISTER) -u "$(INSTALLED)"
	rm -rf "$(INSTALLED)"

clean:
	rm -rf bin
