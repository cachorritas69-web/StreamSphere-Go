#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
curl -fsS "$BASE_URL/health/dependencies"
echo
