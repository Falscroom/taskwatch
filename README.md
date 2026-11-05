# taskwatch

Монитор MR из GitLab и тасок YouTrack в строке меню macOS: карточки «что требует
твоего действия» и одноразовые уведомления о новых событиях. Go + Wails v3.

Инструкция для коллег (установка из архива) — [packaging/README.md](packaging/README.md).

## Сборка и проверка

Нужны Go 1.26+, Xcode Command Line Tools, golangci-lint (для `make lint`) и node
(для тестов фронтенда; без него они пропускаются).

```bash
make build    # bin/taskwatch
make check    # vet + lint + test + тесты фронтенда — должно быть зелёным перед коммитом
make demo     # окно в браузере на поддельных GitLab/YouTrack с примерами карточек
make dist     # dist/taskwatch-mac.zip для раздачи
make install  # поставить себе как LaunchAgent
```

Тест Связки ключей лезет в настоящий Keychain и по умолчанию пропускается:
`TASKWATCH_KEYCHAIN_TEST=1 go test ./internal/keychain`.

## Режимы запуска

```
taskwatch [-config path] [-addr host:port] <command>
  app      иконка в меню-баре + уведомления (по умолчанию)
  web      то же окно в браузере на -addr — для отладки фронта
  run      цикл опроса и уведомления без UI
  once     один проход опроса
  status   таблица тасок, MR, апрувов и сборок
```

Конфиг — `~/.config/taskwatch/config.yaml` (без токенов), токены — в Связке ключей
(сервис `com.taskwatch`) или в `TASKWATCH_GITLAB_TOKEN` / `TASKWATCH_YOUTRACK_TOKEN`
(env главнее Связки ключей). Режим `web` отвечает только на loopback-адреса.
Рядом с конфигом — `state.json` (отправленные уведомления, заглушения) и лог.

## Окно

`internal/ui/frontend` — без сборщика и зависимостей: `index.html`, `app.css` и ES-модули.
Все клики идут через один делегированный обработчик: элемент помечен `data-action="имя"`,
а `js/actions.js` знает, что это имя делает. Карточки рисуют чистые функции
(`js/cards.js`), форма настроек описана таблицей полей (`js/form.js`) — одной и для
заполнения, и для чтения. Вид держит свежим long-poll `GET /api/view?after=<version>`.
Модули помечены `// @ts-check` с JSDoc-типами — редактор проверяет их как TypeScript.

## Раскладка

```
cmd/taskwatch/     точка входа и таблица команд CLI
cmd/taskwatch-demo/  демо окна на поддельных данных (make demo)
internal/          весь код приложения
  domain/          модель и бизнес-правила (Policy, MuteRules); ничего не импортирует
  collect/         сбор снапшота через порты TaskSource/MRSource, параллельная догрузка
  alerts/          события из снапшота и их доставка (Dispatcher: один раз, заглушения, авто-перевод)
  view/            карточки окна
  engine/          цикл опроса, защита от пустых тиков, последний снапшот, подписки
  app/             сборка зависимостей из конфига — единственное место, где создаются клиенты
  config/          структура настроек и yaml, без побочных эффектов
  settings/        загрузка конфига с токенами, сохранение формы, проверка связи
  integrations/    GitLab и YouTrack: клиенты поверх общего httpx и источники для collect
  platform/        macOS: Связка ключей, уведомления; атомарная запись файлов
  ui/httpapi/      HTTP API окна (тонкие хендлеры, long-poll /api/view) и сервер режима web
  ui/tray/         меню-бар на Wails: иконка, окно, цифра «нужно тебе»
  ui/frontend/     окно: HTML/CSS и ES-модули в js/ без сборщика, встраивается в бинарь;
                   тесты чистых модулей — test/, запускает node (make test-frontend)
  testutil/        поддельные GitLab/YouTrack и хранилище секретов для тестов
packaging/         install.sh, uninstall.sh, plist LaunchAgent, README для коллег
```
