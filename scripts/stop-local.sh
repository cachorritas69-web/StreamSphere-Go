#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_FILE="$ROOT_DIR/.run/pids"

if [[ ! -f "$PID_FILE" ]]; then
  echo "No hay procesos registrados."
  exit 0
fi

while read -r pid name; do
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    echo "Detenido: $name ($pid)"
  fi
done < "$PID_FILE"

rm -f "$PID_FILE"
