# nadia_bot 🤖

[![CI](https://github.com/guardante/family-bot/actions/workflows/ci.yml/badge.svg)](https://github.com/guardante/family-bot/actions/workflows/ci.yml)
[![Deploy](https://github.com/guardante/family-bot/actions/workflows/deploy.yml/badge.svg)](https://github.com/guardante/family-bot/actions/workflows/deploy.yml)

Telegram-бот «Надя»: каждое утро присылает погоду на день, а в остальное время
отвечает на вопросы через GLM (Z.AI). Помнит город и контекст разговора.

**Стек:** Go + SQLite + Docker. Long polling — не нужен публичный IP и TLS.
Вся база — один файл, отдельный сервер БД не нужен.

## Возможности

- 🌅 Ежедневный прогноз погоды в заданное время (по часовому поясу города!)
- 🌡 Погода по команде `/weather` (Open-Meteo, бесплатно и без API-ключа)
- 💬 Свободное общение через GLM с памятью последних сообщений
- 🏙 Бот запоминает город и часовой пояс пользователя
- 🔐 Белый список по Telegram ID
- 🗄 Всё состояние в SQLite (WAL-режим): перезапуск контейнера ничего не теряет

## Быстрый старт

### 1. Что нужно получить

| Что | Где |
|---|---|
| Токен бота | [@BotFather](https://t.me/BotFather) → `/newbot` |
| Свой Telegram ID | [@userinfobot](https://t.me/userinfobot) |
| Ключ Z.AI API | [new-api.z.ai](https://new-api.z.ai) → API Keys |

### 2. Настроить и запустить

```bash
cp .env.example .env
# отредактируй .env: BOT_TOKEN, GLM_API_KEY, ALLOWED_TELEGRAM_IDS

docker compose up -d --build
docker compose logs -f bot   # посмотреть, как живёт
```

Готово. Напиши боту `/start`, затем назови город.

## Команды бота

| Команда | Что делает |
|---|---|
| `/start` | Приветствие; при первом запуске спросит город |
| `/weather` | Погода на сегодня прямо сейчас |
| `/city` | Сменить город |
| `/time` | Время утренней рассылки (формат `ЧЧ:ММ`) |
| `/on`, `/off` | Включить/выключить утреннюю рассылку |
| `/forget` | Очистить историю диалога |
| `/help` | Список команд |

Любой другой текст уходит в GLM — отвечают с учётом контекста.

## Настройка (.env)

| Переменная | По умолчанию | Описание |
|---|---|---|
| `BOT_TOKEN` | — | токен от @BotFather, обязателен |
| `GLM_API_KEY` | — | ключ Z.AI, обязателен |
| `GLM_BASE_URL` | `https://api.z.ai/api/paas/v4` | `https://open.bigmodel.cn/api/paas/v4` для китайской платформы |
| `GLM_MODEL` | `glm-4.5-flash` | имя модели (любая доступная flash-модель) |
| `ALLOWED_TELEGRAM_IDS` | пусто | ID через запятую; **пусто = открыт для всех** |
| `SQLITE_PATH` | `data/nadia.db` | путь к файлу базы (в compose: `/data/nadia.db`) |

> Если `/start` молчит — почти наверняка твой ID не в `ALLOWED_TELEGRAM_IDS`.
> Бот чужаков игнорирует молча, чтобы не быть оракулом для спама.

## Архитектура

```
Telegram ──(long polling)──> бот (Go, ~15 МБ RAM)
                              ├─ telegram: приём/отправка, 4 воркера
                              ├─ bot: команды, онбординг, чат
                              ├─ glm: клиент GLM (OpenAI-совместимый /chat/completions)
                              ├─ weather: Open-Meteo — геокодинг + прогноз
                              └─ storage: database/sql + SQLite (pure Go, без CGO)
                                        │
                              один файл data/nadia.db (WAL)
```

- Папка `internal/storage/migrations/*.sql` применяется автоматически при старте.
- Scheduler раз в 30 с проверяет, кому пора слать утренний прогноз; `last_daily_date`
  защищает от дублей и пропусков после перезапуска.
- Часовые пояса вшиты в бинарник (`time/tzdata`), финальный образ — `scratch`.
- SQLite в WAL-режиме: читатели не блокируют писателя, на нашем профиле нагрузки
  (несколько пользователей, десятки сообщений в день) этого достаточно с запасом.

## Полезное

```bash
make build     # собрать бинарник локально
make test      # тесты storage-слоя на настоящей SQLite-базе
make run       # запустить без docker (нужен только .env)
make up        # docker compose up -d --build
make logs      # логи бота
make down      # остановить
```

Обновить версию: пуш в `main` — дальше всё происходит само (см. CI/CD).

### CI/CD (GitHub Actions)

Действия бесплатны: для публичных репозиториев — без лимита, для приватных — 2000 минут/мес.

- **CI** (`ci.yml`) — на каждый пуш и PR: `gofmt`, `go vet`, сборка, тесты.
- **Deploy** (`deploy.yml`) — на пуш в `main`:
  1. Собирает Docker-образ в облаке (amd64 + arm64) и публикует в GHCR
     (`ghcr.io/guardante/family-bot:latest`) — слабому серверу остаётся только скачать.
  2. Если заданы секреты — заходит на сервер по SSH и обновляет бота
     (`scripts/deploy.sh`: git pull → compose pull → up). Секретов нет — шаг молча пропускается.

Настройка автодеплоя: *Settings → Secrets and variables → Actions → New repository secret*:

| Секрет | Что указать |
|---|---|
| `SSH_HOST` | IP сервера |
| `SSH_USER` | пользователь на сервере |
| `SSH_PRIVATE_KEY` | приватный ключ (его публичная часть — в `~/.ssh/authorized_keys` на сервере) |
| `DEPLOY_DIR` | путь к проекту на сервере, например `/home/user/family-bot` |

> **После первого запуска Deploy:** пакет в GHCR по умолчанию создаётся приватным.
> Открой вкладку *Packages → family-bot → Package settings → Change visibility → Public*,
> иначе `docker compose pull` на сервере потребует логин.

Первый запуск на сервере вручную:

```bash
git clone git@github.com:guardante/family-bot.git && cd family-bot
cp .env.example .env && nano .env      # токен, ключ GLM, вайтлист
docker compose pull bot
docker compose up -d --no-build
```

Локальная разработка со сборкой на месте — по-прежнему работает:
`docker compose up -d --build` соберёт образ из локального кода.

### Бэкап

База — один файл, поэтому бэкап прост:

```bash
docker compose stop bot
docker run --rm -v nadia_bot_botdata:/data -v "$PWD":/backup alpine \
  cp /data/nadia.db /backup/
docker compose start bot
```

Сбросить всё вместе с базой: `docker compose down -v` — **удалит данные**.

### Если пользователей станет много

SQLite спокойно тянет десятки тысяч сообщений. Если когда-нибудь понадобится
несколько процессов-писателей или сложная аналитика — тогда есть смысл вернуться
к Postgres: схема в `migrations/` переносится почти без изменений.
