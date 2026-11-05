BIN   := bin/taskwatch
PLIST := $(HOME)/Library/LaunchAgents/com.taskwatch.plist
DIST  := dist/taskwatch-mac

export MACOSX_DEPLOYMENT_TARGET := 14.0
export CGO_CFLAGS := -mmacosx-version-min=14.0
export CGO_LDFLAGS := -mmacosx-version-min=14.0

.PHONY: build test test-frontend vet lint fmt check demo status install uninstall dist clean

build:
	go build -ldflags "-s -w" -trimpath -o $(BIN) ./cmd/taskwatch

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

status: build
	./$(BIN) status

install: build
	mkdir -p $(HOME)/.local/bin $(HOME)/.config/taskwatch
	cp $(BIN) $(HOME)/.local/bin/taskwatch
	sed "s|__HOME__|$(HOME)|g" packaging/com.taskwatch.plist > $(PLIST)
	-@launchctl bootout gui/$$(id -u) $(PLIST) 2>/dev/null
	launchctl bootstrap gui/$$(id -u) $(PLIST)

uninstall:
	-launchctl bootout gui/$$(id -u) $(PLIST)
	rm -f $(PLIST)

# dist — архив для раздачи коллегам: бинарь + скрипты установки + README + исходники в src/.
dist: build
	rm -rf $(DIST) $(DIST).zip
	mkdir -p $(DIST)/src
	cp $(BIN) $(DIST)/taskwatch
	cp packaging/install.sh packaging/uninstall.sh packaging/README.md packaging/com.taskwatch.plist config.example.yaml $(DIST)/
	chmod +x $(DIST)/taskwatch $(DIST)/install.sh $(DIST)/uninstall.sh
	cp -R cmd internal packaging Makefile go.mod go.sum .gitignore .golangci.yml config.example.yaml README.md $(DIST)/src/
	cd dist && zip -rq taskwatch-mac.zip taskwatch-mac -x "*.DS_Store" && rm -rf taskwatch-mac
	@echo "Готово: $(DIST).zip"

clean:
	rm -rf bin dist
