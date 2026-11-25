#!/usr/bin/env bash
# Запускается на сервере из GitHub Actions (deploy.yml).
# Обновляет код, подтягивает свежий образ из GHCR и перезапускает бота.
set -euo pipefail

cd "${DEPLOY_DIR:?Переменная DEPLOY_DIR не задана}"

echo "==> git pull"
git pull --ff-only

echo "==> docker compose pull"
docker compose pull bot

echo "==> docker compose up"
docker compose up -d --no-build --remove-orphans

echo "==> очистка старых образов"
docker image prune -f

echo "==> статус"
docker compose ps
