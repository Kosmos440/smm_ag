# Контракт Go API

[Спецификация OpenAPI](openapi.yaml) описывает реализованные маршруты. Go обрабатывает запросы в `backend/internal/httpapi/`. Общие операции управления находятся в `backend/internal/channels/`, поэтому будущий Telegram-бот сможет использовать их напрямую.

| Маршрут | Назначение |
| --- | --- |
| `GET /api/health`, `GET /api/ready` | Проверка процесса и соединения с PostgreSQL. |
| `GET /api/v1/channels`, `POST /api/v1/channels` | Список и создание каналов. |
| `GET /api/v1/sources`, `POST /api/v1/sources` | Каталог и добавление источников. |
| `POST /api/v1/sources/{id}/probe` | Пробный сбор до пяти материалов. |
| `GET /api/v1/presets`, `POST /api/v1/presets` | Список и сохранение наборов настроек. |
| `GET` и `PUT` по пути `/api/v1/{channels,sources,presets}/{id}` | Просмотр и полная замена данных ресурса. |
| `GET /api/v1/channels/{id}/events`, `GET /api/v1/channels/{id}/publications` | Карточки и история выпуска по каналу. |
| `GET /api/v1/review`, `PUT /api/v1/review/{id}` | Очередь согласования и решение `{"decision":"approve"}` или `{"decision":"reject"}`. |
| `GET /api/v1/site/news` | Опубликованные новости сайта с необязательным параметром `channel_id`. |
| `POST /api/v1/filter-suggestions` | Подбор ключевых слов и стоп-фраз через настроенную модель. |

У списков есть `limit` и `offset`. По умолчанию API отдаёт первые 50 записей. Для создания и обновления отправляйте `Content-Type: application/json`. Чтобы применить пресет, прочитайте его `settings` и передайте их в запрос создания или обновления канала.

Авторизация пока отложена. Используйте маршруты управления на локальном стенде. Токен Telegram задаётся при запуске worker, а настройки канала содержат только имя или ID чата.

[Управление и API](../docs/management.md) · [Архитектура проекта](../docs/architecture.md)
