#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DEST=${1:-"$HOME/lkdr-api"}

if [ -e "$DEST" ]; then
    echo "Каталог уже существует: $DEST" >&2
    echo "Укажи новый путь или предварительно сохрани/переименуй существующий каталог." >&2
    exit 1
fi

if ! command -v git >/dev/null 2>&1; then
    echo "Не найден git. Установи git и повтори запуск." >&2
    exit 1
fi

echo "Клонирую полный проект..."
git clone --depth 1 https://github.com/jfk9w-go/lkdr-api.git "$DEST"

echo "Применяю изменения..."
cp "$SCRIPT_DIR/overlay/example/main.go" "$DEST/example/main.go"

echo "Применяю исправления кодов ошибок ФД..."
python3 "$SCRIPT_DIR/overlay/tools/patch_lkdr_dto.py" "$DEST"
mkdir -p "$DEST/tools"
cp "$SCRIPT_DIR/overlay/tools/update_lkdr_token.py" "$DEST/tools/update_lkdr_token.py"
chmod 700 "$DEST/tools/update_lkdr_token.py"

cd "$DEST"
go mod edit -droprequire=github.com/jfk9w-go/rucaptcha-api
go mod tidy

echo
echo "Готово: $DEST"
echo "Проверь проект командами:"
echo "  cd '$DEST'"
echo "  go test ./..."
echo "  go run ./example"
