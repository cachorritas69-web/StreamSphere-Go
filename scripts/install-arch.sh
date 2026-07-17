#!/usr/bin/env bash
set -euo pipefail

sudo pacman -Syu --needed go gcc ffmpeg git curl make

echo "Dependencias instaladas. Continúa con: cp .env.example .env && make run"
