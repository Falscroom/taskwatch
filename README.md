# taskwatch

Монитор MR из GitLab и тасок YouTrack в строке меню macOS: карточки «что требует
твоего действия» и одноразовые уведомления о новых событиях. Go + Wails v3.

Инструкция для коллег (установка из архива) — [packaging/README.md](packaging/README.md).

## Сборка и проверка

Нужны Go 1.26+, Xcode Command Line Tools и (для `make lint`) golangci-lint.

```bash
make build    # bin/taskwatch
make check    # vet + lint + test — должно быть зелёным перед коммитом
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

## Раскладка

```
cmd/taskwatch/     точка входа и CLI
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
  gui/             меню-бар (Wails) и HTTP API окна
  gui/frontend/    окно: HTML/CSS/JS без сборщика, встраивается в бинарь
  testutil/        поддельные GitLab/YouTrack и хранилище секретов для тестов
packaging/         install.sh, uninstall.sh, plist LaunchAgent, README для коллег
```
