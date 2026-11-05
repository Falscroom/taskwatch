BIN := bin/taskwatch
PLIST := $(HOME)/Library/LaunchAgents/com.taskwatch.plist

export MACOSX_DEPLOYMENT_TARGET := 14.0
export CGO_CFLAGS := -mmacosx-version-min=14.0
export CGO_LDFLAGS := -mmacosx-version-min=14.0

.PHONY: build vet status install uninstall dist

build:
	go build -ldflags "-s -w" -trimpath -o $(BIN) ./cmd/taskwatch

vet:
	go vet ./...

status: build
	./$(BIN) status

install: build
	mkdir -p $(HOME)/.local/bin $(HOME)/.config/taskwatch
	cp $(BIN) $(HOME)/.local/bin/taskwatch
	sed "s|__HOME__|$(HOME)|g" launchd/com.taskwatch.plist > $(PLIST)
	-@launchctl bootout gui/$$(id -u) $(PLIST) 2>/dev/null
	launchctl bootstrap gui/$$(id -u) $(PLIST)

uninstall:
	-launchctl bootout gui/$$(id -u) $(PLIST)
	rm -f $(PLIST)

# dist — архив для раздачи коллегам: бинарь + скрипты установки + README + исходники.
dist: build
	rm -rf dist/taskwatch-mac dist/taskwatch-mac.zip
	mkdir -p dist/taskwatch-mac
	cp $(BIN) dist/taskwatch-mac/taskwatch
	cp config.example.yaml launchd/com.taskwatch.plist dist/taskwatch-mac/
	cp dist/README.md dist/install.sh dist/uninstall.sh dist/taskwatch-mac/
	chmod +x dist/taskwatch-mac/taskwatch dist/taskwatch-mac/install.sh dist/taskwatch-mac/uninstall.sh
	mkdir -p dist/taskwatch-mac/src
	cp -R cmd internal launchd Makefile go.mod go.sum .gitignore config.example.yaml dist/taskwatch-mac/src/
	cd dist && zip -rq taskwatch-mac.zip taskwatch-mac -x "*.DS_Store" && rm -rf taskwatch-mac
	@echo "Готово: dist/taskwatch-mac.zip"
