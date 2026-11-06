#!/bin/bash
# Установка taskwatch.
# Карантин Gatekeeper снимаем ТОЛЬКО с файлов приложения в служебной папке
# внутри домашней директории, а не во всей папке с распакованным архивом.
# Запускать из Терминала: ./install.sh
set -euo pipefail

if [[ "$(uname)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
  echo "Нужен Mac на Apple Silicon (arm64) с macOS 14+."
  exit 1
fi

cd "$(dirname "$0")"

# Фиксированный каталог установки в домашней директории: сюда разворачивается
# всё приложение, сюда же ложатся обновления (запусти install.sh из нового архива).
DEST="$HOME/.local/share/taskwatch"
mkdir -p "$DEST"

# Разворачиваем состав пакета в DEST; папка с архивом остаётся нетронутой.
rm -rf "$DEST/taskwatch.app" "$DEST/taskwatch"  # taskwatch — голый бинарь прежних версий
cp -R taskwatch.app "$DEST/"
for f in com.taskwatch.plist config.example.yaml README.md install.sh uninstall.sh; do
  if [[ -f "$f" ]]; then
    cp -f "$f" "$DEST/"
  fi
done
chmod +x "$DEST/install.sh" "$DEST/uninstall.sh"

# Снимаем карантин строго внутри DEST.
xattr -dr com.apple.quarantine "$DEST" 2>/dev/null || true

mkdir -p "$HOME/.local/bin" "$HOME/.config/taskwatch" "$HOME/Library/LaunchAgents"
# Команда taskwatch в терминале — ссылка на бинарь внутри приложения.
ln -sfn "$DEST/taskwatch.app/Contents/MacOS/taskwatch" "$HOME/.local/bin/taskwatch"

PLIST="$HOME/Library/LaunchAgents/com.taskwatch.plist"
sed "s|__HOME__|$HOME|g" "$DEST/com.taskwatch.plist" > "$PLIST"

# Перезапускаем агент, если он уже был установлен.
launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"

echo ""
echo "Готово: иконка taskwatch появится в строке меню через пару секунд."
echo "Установка развёрнута в: $DEST"
echo "macOS спросит, можно ли taskwatch присылать уведомления, — нажми «Разрешить»."
echo "Дальше: шестерёнка в окне → заполни GitLab (url, логин, токен read_api);"
echo "токен сохранится в Связке ключей, а не в конфиге."
echo "YouTrack можно добавить позже — достаточно одного заполненного сервиса."
echo "Конфиг (без токенов): ~/.config/taskwatch/config.yaml"
echo "Лог: ~/.config/taskwatch/taskwatch.log"
