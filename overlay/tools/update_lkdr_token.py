#!/usr/bin/env python3
"""Safely update the lkdr-api access token from a Firefox HAR export.

Usage:
  python3 update_lkdr_token.py /path/to/network.har
  python3 update_lkdr_token.py /path/to/network.har --phone 79990000000

The token itself is never printed. HAR files contain credentials; keep them private.
"""

import argparse
import base64
import json
import os
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlparse

DEFAULT_TOKEN_FILE = Path.home() / ".local/share/lkdr/tokens.json"


def jwt_expiry(token: str) -> datetime:
    parts = token.split(".")
    if len(parts) != 3:
        raise ValueError("not a JWT")
    payload = parts[1]
    payload += "=" * (-len(payload) % 4)
    data = json.loads(base64.urlsafe_b64decode(payload))
    return datetime.fromtimestamp(int(data["exp"]), timezone.utc)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("har_file", type=Path)
    parser.add_argument("--phone", default=os.environ.get("LKDR_PHONE"))
    parser.add_argument("--token-file", type=Path, default=DEFAULT_TOKEN_FILE)
    args = parser.parse_args()

    if not args.har_file.is_file():
        parser.error(f"HAR-файл не найден: {args.har_file}")

    try:
        har = json.loads(args.har_file.read_text(encoding="utf-8"))
    except Exception as exc:
        print(f"Не удалось прочитать HAR: {exc}")
        return 1

    candidates = []
    for entry in har.get("log", {}).get("entries", []):
        request = entry.get("request", {})
        host = (urlparse(request.get("url", "")).hostname or "").lower()
        if not host.endswith("nalog.ru"):
            continue
        for header in request.get("headers", []):
            if header.get("name", "").lower() != "authorization":
                continue
            value = header.get("value", "").strip()
            if not value.lower().startswith("bearer "):
                continue
            token = value[7:].strip()
            try:
                expiry = jwt_expiry(token)
            except Exception:
                continue
            candidates.append((entry.get("startedDateTime", ""), token, expiry))

    if not candidates:
        print("В HAR не найден Bearer JWT с полем exp.")
        return 1

    candidates.sort(key=lambda item: item[0], reverse=True)
    _, token, expiry = candidates[0]
    now = datetime.now(timezone.utc)
    if expiry <= now:
        print("Самый свежий найденный токен уже истёк:", expiry.isoformat())
        return 1

    token_file = args.token_file.expanduser()
    token_file.parent.mkdir(parents=True, exist_ok=True, mode=0o700)

    old = {}
    if token_file.exists():
        try:
            old = json.loads(token_file.read_text(encoding="utf-8"))
        except Exception:
            print("Существующий файл токенов повреждён; оставляю его без изменений.")
            return 1

    tokens = {
        "refreshToken": old.get("refreshToken", ""),
        "token": token,
        "tokenExpireIn": expiry.isoformat().replace("+00:00", "Z"),
    }
    if old.get("refreshTokenExpiresIn"):
        tokens["refreshTokenExpiresIn"] = old["refreshTokenExpiresIn"]

    # Keep compatibility with the existing flat token file unless phone is supplied.
    if args.phone:
        root = {}
        if isinstance(old, dict) and "token" not in old:
            root = old
        root[args.phone] = tokens
        output = root
    else:
        output = tokens

    fd, temp_name = tempfile.mkstemp(prefix=".tokens-", suffix=".tmp", dir=token_file.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(output, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
        os.chmod(temp_name, 0o600)
        os.replace(temp_name, token_file)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)

    print("Токен сохранён:", token_file)
    print("Срок действия:", tokens["tokenExpireIn"])
    print("Refresh token сохранён:", bool(tokens["refreshToken"]))
    print("Важно: если refresh token отсутствует, после истечения access token потребуется повторная авторизация.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
