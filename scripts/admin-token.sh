#!/bin/sh
# 生成后台令牌（HMAC）
#
# 用法：
#   ./scripts/admin-token.sh <admin_user_id> <admin_sign_key>
#
# 示例：
#   ./scripts/admin-token.sh admin "your-admin-sign-key"
#
# 然后请求时带上：
#   curl -H "X-Api-Token: <token>" -X POST http://127.0.0.1:8080/api/admin/xxx

set -e

if [ -z "$1" ] || [ -z "$2" ]; then
    echo "usage: $0 <admin_user_id> <admin_sign_key>" >&2
    exit 1
fi

USER_ID="$1"
APP_KEY="$2"
TS=$(date +%s)

SIG=$(printf "%s:%s" "$USER_ID" "$TS" | openssl dgst -sha256 -hmac "$APP_KEY" -hex | sed 's/^.* //')
RAW="${TS}:${USER_ID}:${SIG}"

printf "%s" "$RAW" | base64 | tr '+/' '-_' | tr -d '=\n'
echo
