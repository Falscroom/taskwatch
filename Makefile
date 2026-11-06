BIN   := bin/taskwatch
APP   := bin/taskwatch.app
DMG   := dist/taskwatch.dmg

# SIGN — чем подписывать taskwatch.app. Если в Связке ключей есть сертификат
# подписи кода «taskwatch» — им: подпись одна на все сборки, и после обновления
# macOS не спрашивает заново доступ к Связке ключей и разрешение на уведомления.
# Иначе — ad-hoc («-»), своя у каждой сборки.
SIGN ?= $(shell security find-certificate -c taskwatch >/dev/null 2>&1 && echo taskwatch || echo -)

export MACOSX_DEPLOYMENT_TARGET := 14.0
export CGO_CFLAGS := -mmacosx-version-min=14.0
export CGO_LDFLAGS := -mmacosx-version-min=14.0

.PHONY: build app icon test test-frontend vet lint fmt check demo demo-app status install uninstall dmg dist clean

build:
	go build -ldflags "-s -w" -trimpath -o $(BIN) ./cmd/taskwatch

# app — taskwatch.app: бинарь + Info.plist + иконка, подписанные SIGN. Уведомления
# Центра уведомлений и «Объекты входа» работают только из такого бандла.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp $(BIN) $(APP)/Contents/MacOS/taskwatch
	cp packaging/Info.plist $(APP)/Contents/Info.plist
	cp packaging/taskwatch.icns $(APP)/Contents/Resources/taskwatch.icns
	codesign --force --sign "$(SIGN)" $(APP)

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

# install — поставить себе: taskwatch.app в «Программы» и запустить. На первом
# запуске приложение само включает запуск при входе.
install: app
	-@osascript -e 'quit app "taskwatch"' 2>/dev/null
	rm -rf /Applications/taskwatch.app
	cp -R $(APP) /Applications/
	open /Applications/taskwatch.app

uninstall:
	-@osascript -e 'quit app "taskwatch"' 2>/dev/null
	rm -rf /Applications/taskwatch.app

# dmg — образ для раздачи: taskwatch.app, ярлык «Программы» для перетаскивания
# и инструкция.
dmg: app
	rm -rf dist/dmg $(DMG)
	mkdir -p dist/dmg
	cp -R $(APP) dist/dmg/
	ln -s /Applications dist/dmg/Applications
	cp packaging/README.md dist/dmg/Инструкция.md
	hdiutil create -volname taskwatch -srcfolder dist/dmg -ov -format UDZO $(DMG) >/dev/null
	rm -rf dist/dmg
	@echo "Готово: $(DMG)"

dist: dmg

clean:
	rm -rf bin dist
