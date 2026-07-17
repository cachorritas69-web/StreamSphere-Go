#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
STAMP="$(date +%s)"
USERNAME="smoke${STAMP}"
EMAIL="smoke${STAMP}@example.com"
PASSWORD="Smoke123!"

command -v curl >/dev/null || { echo "curl no está instalado"; exit 1; }
command -v python3 >/dev/null || { echo "python3 no está instalado"; exit 1; }

echo "1/6 Salud del Gateway"
curl -fsS "$BASE_URL/health" >/dev/null

echo "2/6 Registro"
REGISTER_RESPONSE="$(curl -fsS -X POST "$BASE_URL/api/auth/register" -H 'Content-Type: application/json' -d "{\"username\":\"$USERNAME\",\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")"
TOKEN="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["accessToken"])' <<<"$REGISTER_RESPONSE")"

echo "3/6 Perfil"
curl -fsS "$BASE_URL/api/users/me" -H "Authorization: Bearer $TOKEN" >/dev/null

echo "4/6 Crear canal"
CHANNEL_RESPONSE="$(curl -fsS -X POST "$BASE_URL/api/channels" -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" -d "{\"name\":\"Canal $USERNAME\",\"description\":\"Creado por la prueba automática\"}")"
CHANNEL_ID="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["channel"]["channelId"])' <<<"$CHANNEL_RESPONSE")"
TOKEN="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["accessToken"])' <<<"$CHANNEL_RESPONSE")"

echo "5/6 Crear metadatos de video"
VIDEO_RESPONSE="$(curl -fsS -X POST "$BASE_URL/api/videos" -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" -d "{\"channelId\":\"$CHANNEL_ID\",\"title\":\"Video de prueba $STAMP\",\"description\":\"Prueba automática\",\"category\":\"Tecnología\",\"tags\":[\"smoke\",\"go\"],\"visibility\":\"PUBLIC\"}")"
VIDEO_ID="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["videoId"])' <<<"$VIDEO_RESPONSE")"

echo "6/6 Consultar videos propios"
curl -fsS "$BASE_URL/api/videos/mine" -H "Authorization: Bearer $TOKEN" | grep -q "$VIDEO_ID"

echo "OK: registro, JWT, canal y catálogo funcionan. Video creado: $VIDEO_ID"
