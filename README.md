# taskwatch

Монитор MR из GitLab и тасок YouTrack в строке меню macOS: показывает, что ждёт
твоего действия, и присылает уведомление о каждом новом событии один раз.

- Твои MR, которым нужна работа: упала сборка, конфликт, запрошены изменения, отстал от target.
- MR, которые ждут твоего ревью.
- Апрувы собраны — кнопка перевода таски командой YouTrack (или авто-перевод).
- Твой MR попал в релизную ветку — следит за сборкой релиза («Draft: Release …») с ним.
- Нативные уведомления: клик открывает MR, кнопки «Заглушить на час» и «→ статус».
- Токены — в Связке ключей, в `~/.config/taskwatch/config.yaml` только настройки.

## Установка

Нужны Mac на Apple Silicon с macOS 14+, Go 1.26+ и Xcode Command Line Tools.

```bash
make install
```

Собирает `taskwatch.app`, кладёт его в `~/.local/share/taskwatch` и запускает как
LaunchAgent. Дальше: иконка в строке меню → шестерёнка → GitLab (URL, логин, токен
`read_api`); YouTrack по желанию — запрос тасок подставит кнопка «Подобрать».

Архив для раздачи — `make dist` (внутри `install.sh`, инструкция —
[packaging/README.md](packaging/README.md)).

## Разработка

```bash
make check     # vet + lint + тесты Go и фронтенда
make demo      # окно в браузере на поддельных GitLab/YouTrack: http://127.0.0.1:7778
make demo-app  # то же демо в меню-баре
```

Тесты не трогают настоящую Связку ключей и не шлют уведомлений. Для `make lint`
нужен golangci-lint v2, для тестов фронтенда — node.

| Где | Что |
|---|---|
| `internal/domain` | модель и правила: что «требует работы», «пора переводить», «ждёт ревью» |
| `internal/collect`, `internal/integrations` | сбор данных из GitLab и YouTrack |
| `internal/alerts`, `internal/engine` | события, доставка уведомлений, цикл опроса |
| `internal/ui` | HTTP API окна, меню-бар на Wails, фронтенд без сборщика |
| `cmd/taskwatch` | точка входа: `app`, `web`, `run`, `once`, `status` |
| `packaging/` | `install.sh`, LaunchAgent, `Info.plist`, иконка |
