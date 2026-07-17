#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_DIR="$ROOT_DIR/.run"
BIN_DIR="$RUN_DIR/bin"
LOG_DIR="$RUN_DIR/logs"
PID_FILE="$RUN_DIR/pids"

cd "$ROOT_DIR"
mkdir -p "$BIN_DIR" "$LOG_DIR" "$ROOT_DIR/data" "$ROOT_DIR/media"

if [[ -f "$PID_FILE" ]]; then
  while read -r pid _; do
    if kill -0 "$pid" 2>/dev/null; then
      echo "StreamSphere ya parece estar ejecutándose. Usa: make stop"
      exit 1
    fi
  done < "$PID_FILE"
fi
rm -f "$PID_FILE"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

export JWT_SECRET="${JWT_SECRET:-streamsphere-local-jwt-secret-change-me}"
export SERVICE_KEY="${SERVICE_KEY:-streamsphere-local-service-key-change-me}"
export ALLOWED_ORIGIN="${ALLOWED_ORIGIN:-http://localhost:3000}"
export PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-http://localhost:8080}"
export MAX_UPLOAD_MB="${MAX_UPLOAD_MB:-100}"

for command in go gcc curl; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "Falta la dependencia: $command"
    echo "En Arch Linux ejecuta: bash scripts/install-arch.sh"
    exit 1
  }
done

if ! command -v ffmpeg >/dev/null 2>&1; then
  echo "Aviso: FFmpeg no está instalado; la plataforma iniciará, pero no podrá procesar videos."
fi

echo "Descargando módulos de Go..."
go mod download

echo "Compilando servicios..."
services=(auth catalog notification analytics media playback social gateway frontend)
for service in "${services[@]}"; do
  go build -o "$BIN_DIR/$service" "./services/$service"
done

cleanup_on_error() {
  if [[ -f "$PID_FILE" ]]; then
    while read -r pid _; do kill "$pid" 2>/dev/null || true; done < "$PID_FILE"
  fi
}
trap cleanup_on_error ERR INT TERM

start_service() {
  local name="$1"
  shift
  echo "Iniciando $name..."
  nohup env "$@" "$BIN_DIR/$name" >"$LOG_DIR/$name.log" 2>&1 &
  echo "$! $name" >> "$PID_FILE"
}

start_service auth \
  PORT=8081 DB_PATH="$ROOT_DIR/data/auth.db"
start_service catalog \
  PORT=8082 DB_PATH="$ROOT_DIR/data/catalog.db" AUTH_SERVICE_URL=http://localhost:8081
start_service notification \
  PORT=8087 DB_PATH="$ROOT_DIR/data/notification.db"
start_service analytics \
  PORT=8086 DB_PATH="$ROOT_DIR/data/analytics.db"
start_service media \
  PORT=8083 DB_PATH="$ROOT_DIR/data/media.db" MEDIA_ROOT="$ROOT_DIR/media" \
  CATALOG_SERVICE_URL=http://localhost:8082 NOTIFICATION_SERVICE_URL=http://localhost:8087
start_service playback \
  PORT=8084 DB_PATH="$ROOT_DIR/data/playback.db" \
  CATALOG_SERVICE_URL=http://localhost:8082 ANALYTICS_SERVICE_URL=http://localhost:8086
start_service social \
  PORT=8085 DB_PATH="$ROOT_DIR/data/social.db" \
  CATALOG_SERVICE_URL=http://localhost:8082 NOTIFICATION_SERVICE_URL=http://localhost:8087
start_service gateway \
  PORT=8080 AUTH_SERVICE_URL=http://localhost:8081 CATALOG_SERVICE_URL=http://localhost:8082 \
  MEDIA_SERVICE_URL=http://localhost:8083 PLAYBACK_SERVICE_URL=http://localhost:8084 \
  SOCIAL_SERVICE_URL=http://localhost:8085 ANALYTICS_SERVICE_URL=http://localhost:8086 \
  NOTIFICATION_SERVICE_URL=http://localhost:8087
start_service frontend \
  PORT=3000 FRONTEND_DIR="$ROOT_DIR/frontend"

wait_for() {
  local name="$1"
  local url="$2"
  for _ in $(seq 1 40); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "  ✓ $name"
      return 0
    fi
    sleep 0.5
  done
  echo "  ✗ $name no respondió. Revisa $LOG_DIR/$name.log"
  return 1
}

echo "Verificando salud..."
wait_for auth http://localhost:8081/health
wait_for catalog http://localhost:8082/health
wait_for notification http://localhost:8087/health
wait_for analytics http://localhost:8086/health
wait_for media http://localhost:8083/health
wait_for playback http://localhost:8084/health
wait_for social http://localhost:8085/health
wait_for gateway http://localhost:8080/health
wait_for frontend http://localhost:3000/health

trap - ERR INT TERM
cat <<MSG

StreamSphere está ejecutándose:
  Web:     http://localhost:3000
  Gateway: http://localhost:8080
  Salud:   http://localhost:8080/health/dependencies

Usuarios de demostración:
  demo@streamsphere.local  / Demo123!
  admin@streamsphere.local / Admin123!

Logs: $LOG_DIR
Detener: make stop
MSG
