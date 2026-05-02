# Hotel Booking HTMX (Go) - compact version

Учебный проект для Практики 2 по архитектуре систем.

Приложение реализует сервис бронирования отелей в стиле **Clean Architecture**:

- `domain` - сущности и бизнес-правила
- `ports` - интерфейсы зависимостей
- `usecases` - прикладные сценарии
- `adapters/http` - HTTP + HTML/HTMX адаптеры
- `repositories/inmemory` - in-memory хранилище
- `infrastructure` - системные сервисы

## Что умеет приложение

- искать свободные номера
- создавать временную бронь (`hold`)
- подтверждать бронь с данными гостя
- отменять бронь
- открывать карточку брони по ID

## Как запустить в VS Code

1. Откройте папку проекта в VS Code.
2. Убедитесь, что установлен Go 1.22+.
3. Встроенный терминал VS Code:

```bash
go test ./...
go run .
```

4. Откройте в браузере:

```text
http://localhost:8080
```

## Основной сценарий руками

1. На главной странице задайте город, даты и число гостей.
2. Нажмите **Найти свободные номера**.
3. Выберите карточку номера и нажмите **Создать временную бронь (hold)**.
4. Введите данные гостя и нажмите **Подтвердить бронь**.
5. Для просмотра карточки по ID используйте отдельную форму на главной странице или откройте URL `/bookings/{id}` вручную.
6. Для сценария ошибки нажмите повторную отмену уже отмененной брони или попробуйте подтвердить hold без данных гостя.

## Архитектурная идея по слоям

### Domain

`app/domain/model.go`

Что лежит внутри:

- `Room` - номер в каталоге
- `Guest` - данные гостя
- `Booking` - бронь
- `BookingStatus` - жизненный цикл брони
- ошибки предметной области
- методы `Confirm`, `Cancel`, `IsExpired`

Почему это внутренний слой:

- он не знает про HTTP
- он не знает про HTMX
- он не знает про `map`, `JSON`, `HTML`

### Ports

`app/ports/ports.go`

Это контракты, от которых зависят use cases.

### Use Cases

`app/usecases/usecases.go`

Главные сценарии:

- `FindAvailableRoomsUseCase`
- `HoldBookingUseCase`
- `ConfirmBookingUseCase`
- `CancelBookingUseCase`
- `GetBookingUseCase`

### Adapters / HTTP

`app/adapters/http/web.go`

Этот слой принимает HTTP-запросы, читает параметры, вызывает use case и возвращает HTML-страницу или HTML-фрагмент.

### Repository / Infrastructure

`app/repositories/inmemory/repository.go`

- хранит номера и брони в `map`
- проверяет пересечение дат
- не пропускает активные hold/confirmed брони

`app/infrastructure/services.go`

- `SystemClock` - текущее время
- `SimplePriceCalculator` - расчет цены за период
- `RandomIDGenerator` - генератор ID
- `SeedRooms()` - стартовые данные

## Направление зависимостей

```text
HTTP handler -> use case -> ports(interface) -> repository/service implementation
```

```text
domain <- ports <- usecases <- adapters <- main
```

## Интеграционный тест

Есть файл `workflow_test.go`.

Он проверяет полный workflow:

1. поиск номера
2. создание hold
3. подтверждение
4. чтение брони
5. отмена


## Дополнительно

В корне проекта лежит файл `openapi_rest_reference.yaml` - это справочный REST-контракт той же бизнес-логики.
