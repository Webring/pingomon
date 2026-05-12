# pingomon
<p align="center">
  <img src="assets/logo.png" width="400" height="400" alt="Pingomon logo" />
</p>

## О проекте
Мониторинг состояния вашего сервиса. Адреса хранятся в Postgres и их можно добавлять прямо через телеграм бота. Пинг воркер забирает актуальный список адресов и пишет метрики в ClickHouse.
Отдельный уведомляющий воркер читает ошибки за окно времени и шлет алерты подписчикам.


## Запуск
### 1. Склонировать репозиторий
```shell
git clone https://github.com/Webring/pingomon.git
cd pingomon
```

### 2. Заполнить переменные окружения
Нужно создать файл `deploy/.env` (можно скопировать `deploy/.env.example`):
```shell
cp ./deploy/.env.example ./deploy/.env
```
В файле `.env` нужно заполнить данные, особенно обратить внимание на строки, где есть `#change`.
Добавлены настройки Postgres (`POSTGRES_*`, `POSTGRES_DSN`) — туда бот кладет новые адреса.
Для алертов есть настройки `NOTIFY_WINDOW_MINUTES` и `NOTIFY_POLL_SECONDS`.
Для Telegram Web App нужно указать публичный `WEBAPP_BASE_URL`, а локальный адрес API настраивается через `WEBAPP_LISTEN_ADDR`.
Если страница и API живут на разных доменах, задай:
`WEBAPP_API_BASE_URL=https://api.mydomain.com`
`WEBAPP_ALLOWED_ORIGIN=https://webapp.mydomain.com`
Если хочешь открыть dashboard не из Telegram, для локальной отладки можно временно выставить `WEBAPP_DEV_USER_ID=<telegram_user_id>`.

### GitHub Pages для Web App
Есть workflow [deploy-pages.yml](/Users/webring/GolandProjects/pingomon/.github/workflows/deploy-pages.yml), который публикует `cmd/webapp_static/index.html` на GitHub Pages.

Что нужно настроить в GitHub:
- В `Settings -> Pages` выбрать `GitHub Actions` как source.
- В `Settings -> Secrets and variables -> Actions -> Variables` добавить `WEBAPP_API_BASE_URL`, например `https://api.mydomain.com`.
- В `deploy/.env` для backend указать `WEBAPP_ALLOWED_ORIGIN` на домен Pages или на привязанный кастомный домен, например `https://webring.github.io` или `https://webapp.mydomain.com`.

После пуша в `main` workflow соберет статическую страницу и выложит ее на GitHub Pages.
Go-сервис на `WEBAPP_LISTEN_ADDR` теперь отдает только API и не раздает HTML-страницу.

### 3. Применить миграции и подготовить базу данных
```
make prepare
```
Команда поднимет ClickHouse, применит миграции и остановит контейнер.
Для применения миграций должен стоять [goose](https://pressly.github.io/goose/installation/). 
Если `goose` не установлен, то можно применить миграции вручную через файлы в `./db/migrations/`.
### 4. Запуск сервиса
```shell
make up
```

## Результат
По команде `/stats` бот покажет статистику только по твоим подпискам:
```
📊 Ping statistics:
• https://ya.ru
  avg: 327.27 ms | min: 206.94 | max: 473.76
• http://github.com/
  avg: 486.13 ms | min: 290.70 | max: 1161.95
• https://nstu.ru
  avg: 956.12 ms | min: 678.15 | max: 1175.98
```

Команды бота:
- `/add <url>` — добавить адрес в свои подписки (сохраняется в Postgres).
- `/subs` — показать все твои подписки.
- `/del <url>` — удалить подписку.
- `/stats` — показать метрики по своим адресам.
- `/app` — открыть Telegram Web App с графиком задержек, доступностью и историей инцидентов.

Воркер берет уникальные адреса из Postgres (даже если они добавлены разными пользователями) и пингует их, результаты кладет в ClickHouse.
Notification worker раз в `NOTIFY_POLL_SECONDS` смотрит ошибки за последние `NOTIFY_WINDOW_MINUTES` и шлет всем подписчикам: сколько ошибок было и какие уникальные тексты.
Web App показывает данные только по подпискам текущего Telegram-пользователя и авторизуется через `initData` Telegram Web App.
