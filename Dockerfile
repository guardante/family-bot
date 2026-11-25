# --- Сборка ---
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /nadia ./cmd/bot \
	&& mkdir -p /data && chown 65534:65534 /data

# --- Запуск ---
# scratch: нет ничего лишнего. CA-сертификаты и tzdata вшиты отдельно.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /nadia /nadia
# Каталог для SQLite-базы; владелец — непривилегированный пользователь.
# Docker перенесёт эти права внутрь именованного volume при первом запуске.
COPY --from=build --chown=65534:65534 /data /data

USER 65534:65534
ENTRYPOINT ["/nadia"]
