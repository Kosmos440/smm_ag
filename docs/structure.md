# Карта репозитория

| Путь | Назначение |
| --- | --- |
| [compose.yaml](../compose.yaml), [.env.example](../.env.example) | Сервисы локального стенда, переменные и профиль интеграционных тестов |
| [backend/Dockerfile](../backend/Dockerfile), [backend/go.mod](../backend/go.mod) | Сборка образа Go, версии модуля и зависимостей |
| [backend/cmd/api](../backend/cmd/api) | Точка запуска API, настройка адаптеров, модели, PostgreSQL и HTTP-сервера |
| [backend/cmd/worker](../backend/cmd/worker) | Точка запуска worker, расписание, параллельный опрос, вызов правил и доставка |
| [backend/internal/config](../backend/internal/config) | Чтение параметров окружения и значения по умолчанию |
| [backend/internal/model](../backend/internal/model) | Общие типы источника, материала, канала, события и публикации |
| [backend/internal/httpapi](../backend/internal/httpapi) | Маршруты, служебные проверки, валидация HTTP и встроенная панель `console/` |
| [backend/internal/channels](../backend/internal/channels) | Операции над каталогом, каналами, пресетами и согласованием, пробный сбор и генерация фильтров |
| [backend/internal/ingest](../backend/internal/ingest) | Общий интерфейс сборщика, HTTP-клиент, разбор HTML, нормализация текста и даты |
| [backend/internal/ingest/catalog](../backend/internal/ingest/catalog) | Связь типа источника с одним из адаптеров |
| [backend/internal/ingest/rss](../backend/internal/ingest/rss), [website](../backend/internal/ingest/website), [telegram](../backend/internal/ingest/telegram) | Преобразование ответа каждого типа источника в общий формат материала |
| [backend/internal/news](../backend/internal/news) | Фильтрация, извлечение фактов, сопоставление событий и расчёт срочности |
| [backend/internal/postgres](../backend/internal/postgres) | Запросы к PostgreSQL, транзакции обработки, очередь, опрос источников и блокировки |
| [backend/internal/publishing](../backend/internal/publishing) | Правила окна выпуска, интерфейс отправки и реализации для сайта и Telegram |
| [backend/migrations](../backend/migrations) | SQL-миграции и Go-код их применения |
| [api/openapi.yaml](../api/openapi.yaml) | Контракт HTTP API |
| [frontend](../frontend) | Каркас каталога для будущего отдельного интерфейса. Рабочая панель находится в Go API |

Тесты лежат рядом с кодом. В `cmd/worker` проверяется сквозной сценарий с реальной PostgreSQL, синтетическим источником и тестовым отправителем. Интеграционный тест в `internal/postgres` проверяет сохранение материала, очередь и историю состояний. Тесты адаптеров, фильтрации, HTTP API и формата запроса Telegram используют локальные фикстуры или подменённый транспорт. [Запуск тестов](backend-local.md#проверки)

## Медиа, этап 2

backend/internal/media отвечает за ограниченное скачивание оригиналов и запись/чтение S3. backend/internal/ingest/media.go извлекает HTML-вложения. backend/internal/publishing/telegram/media.go собирает multipart на диске и отправляет фото, видео и альбомы. backend/migrations/004_media.sql сохраняет ссылки на вложения. Сервис media в compose.yaml запускает SeaweedFS. [Подробности](media.md).

## Классификация темы
backend/internal/topic реализует ключи, векторы и LLM. backend/internal/postgres/topics.go отвечает за словоформы, очередь, кеш и историю оценок. backend/cmd/worker/topics.go читает статьи и соединяет классификатор с очередью событий. backend/internal/httpapi/topics.go обслуживает историю и редакторскую оценку. [Поток данных](topic-classification.md)

## Общий каталог событий

- [Алгоритм сопоставления](../backend/internal/news/catalog.go) сравнивает словоформы заголовка, юрисдикцию и стадию чтения.
- [Хранилище каталога](../backend/internal/postgres/event_catalog.go) читает сохранённые оценки и записывает события, версии и связи с материалами.
- [Команды worker](../backend/cmd/worker/event_catalog.go) запускают архивный прогон и фоновое пополнение каталога. [Поток данных](event-catalog.md)

## Локальная LLM

Сервис `llm` в [Compose](../compose.yaml) запускает llama.cpp в дополнительном профиле `llm-local`. API и worker обращаются к нему через существующие клиенты Chat Completions. [Настройка](topic-classification.md#локальная-llm-в-compose)
