#!/bin/bash
# Полное удаление taskwatch: агент, бинарь, каталог установки, токены из
# Связки ключей. Конфиг с настройками не трогаем.
set -euo pipefail

PLIST="$HOME/Library/LaunchAgents/com.taskwatch.plist"
launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
"$HOME/.local/bin/taskwatch" secret-clear 2>/dev/null || true
rm -f "$PLIST" "$HOME/.local/bin/taskwatch"
rm -rf "$HOME/.local/share/taskwatch"

echo "taskwatch остановлен и удалён."
echo "Конфиг с токенами остался в ~/.config/taskwatch — удали руками, если не нужен: rm -rf ~/.config/taskwatch"
