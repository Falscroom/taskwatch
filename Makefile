BIN   := bin/taskwatch
APP   := bin/taskwatch.app
PLIST := $(HOME)/Library/LaunchAgents/com.taskwatch.plist
SHARE := $(HOME)/.local/share/taskwatch
DIST  := dist/taskwatch-mac

export MACOSX_DEPLOYMENT_TARGET := 14.0
export CGO_CFLAGS := -mmacosx-version-min=14.0
export CGO_LDFLAGS := -mmacosx-version-min=14.0

.PHONY: build app icon test test-frontend vet lint fmt check demo demo-app status install uninstall dist clean

build:
	go build -ldflags "-s -w" -trimpath -o $(BIN) ./cmd/taskwatch

# app — taskwatch.app: бинарь + Info.plist + иконка, подпись ad-hoc. Уведомления
# Центра уведомлений macOS работают только из такого бандла.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp $(BIN) $(APP)/Contents/MacOS/taskwatch
	cp packaging/Info.plist $(APP)/Contents/Info.plist
	cp packaging/taskwatch.icns $(APP)/Contents/Resources/taskwatch.icns
	codesign --force --sign - $(APP)

# icon — пересобрать packaging/taskwatch.icns из генератора packaging/icon.
icon:
	rm -rf bin/icon.iconset && mkdir -p bin/icon.iconset
	go run packaging/icon/main.go -o bin/icon.png
	for s in 16 32 128 256 512; do \
		sips -z $$s $$s bin/icon.png --out bin/icon.iconset/icon_$${s}x$${s}.png >/dev/null; \
		sips -z $$((s*2)) $$((s*2)) bin/icon.png --out bin/icon.iconset/icon_$${s}x$${s}@2x.png >/dev/null; \
	done
	iconutil -c icns bin/icon.iconset -o packaging/taskwatch.icns

test:
	go test ./...

# test-frontend — тесты чистых JS-модулей окна; нужен node, без него пропускаются.
test-frontend:
	@if command -v node >/dev/null; then cd internal/ui/frontend && node --test 'test/*.test.js'; \
	else echo "node не найден — тесты фронтенда пропущены"; fi

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -w cmd internal

# check — всё, что должно быть зелёным перед коммитом.
check: vet lint test test-frontend

# demo — окно в браузере на поддельных GitLab и YouTrack с примерами карточек:
# для работы над фронтендом без настоящих аккаунтов.
demo:
	go run ./cmd/taskwatch-demo

# demo-app — то же демо в меню-баре, как настоящее приложение (окно Wails).
demo-app:
	go run ./cmd/taskwatch-demo -app

status: build
	./$(BIN) status

install: app
	mkdir -p $(HOME)/.local/bin $(HOME)/.config/taskwatch $(SHARE)
	rm -rf $(SHARE)/taskwatch.app
	cp -R $(APP) $(SHARE)/
	ln -sfn $(SHARE)/taskwatch.app/Contents/MacOS/taskwatch $(HOME)/.local/bin/taskwatch
	sed "s|__HOME__|$(HOME)|g" packaging/com.taskwatch.plist > $(PLIST)
	-@launchctl bootout gui/$$(id -u) $(PLIST) 2>/dev/null
	launchctl bootstrap gui/$$(id -u) $(PLIST)

uninstall:
	-launchctl bootout gui/$$(id -u) $(PLIST)
	rm -f $(PLIST)

# dist — архив для раздачи коллегам: taskwatch.app + скрипты установки + README + исходники в src/.
dist: app
	rm -rf $(DIST) $(DIST).zip
	mkdir -p $(DIST)/src
	cp -R $(APP) $(DIST)/
	cp packaging/install.sh packaging/uninstall.sh packaging/README.md packaging/com.taskwatch.plist config.example.yaml $(DIST)/
	chmod +x $(DIST)/install.sh $(DIST)/uninstall.sh
	cp -R cmd internal packaging Makefile go.mod go.sum .gitignore .golangci.yml config.example.yaml README.md $(DIST)/src/
	cd dist && zip -rq taskwatch-mac.zip taskwatch-mac -x "*.DS_Store" && rm -rf taskwatch-mac
	@echo "Готово: $(DIST).zip"

clean:
	rm -rf bin dist
