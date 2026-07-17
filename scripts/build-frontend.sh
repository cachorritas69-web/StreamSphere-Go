#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DIR="$ROOT_DIR/frontend"
OUTPUT_DIR="$SOURCE_DIR/dist"
HOST="${STREAMSPHERE_API_HOST:-localhost:8080}"

if [[ "$HOST" == http://* || "$HOST" == https://* ]]; then
  API_URL="${HOST%/}"
elif [[ "$HOST" == localhost:* || "$HOST" == 127.0.0.1:* ]]; then
  API_URL="http://$HOST"
else
  API_URL="https://$HOST"
fi

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"
cp "$SOURCE_DIR/index.html" "$SOURCE_DIR/styles.css" "$SOURCE_DIR/app.js" "$OUTPUT_DIR/"
printf "window.STREAMSPHERE_API = '%s';\n" "$API_URL" > "$OUTPUT_DIR/config.js"

echo "Frontend preparado en $OUTPUT_DIR"
echo "API configurada: $API_URL"
