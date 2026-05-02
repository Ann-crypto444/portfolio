package ports

import (
	"hotelbooking/app/domain"
	"time"
)

// Пакет ports содержит интерфейсы, от которых зависят use case'ы.
//
// Это главный механизм инверсии зависимостей в проекте:
//
//   - use case знает только контракт;
//   - конкретная реализация живёт во внешнем слое.

// AvailabilityReader умеет искать свободные номера по фильтру.
// В текущем проекте его реализует inmemory.Repository.
type AvailabilityReader interface {
	FindAvailableRooms(filter domain.RoomSearchFilter, now domain.Moment) ([]domain.Room, error)
}

// AvailabilityChecker проверяет доступность конкретного номера.
type AvailabilityChecker interface {
	IsRoomAvailable(roomID string, from, to, now domain.Moment) (bool, error)
}

// RoomReader читает номер по ID.
type RoomReader interface {
	GetRoomByID(roomID string) (domain.Room, error)
}

// BookingRepository описывает все операции с бронями.
type BookingRepository interface {
	CreateBooking(booking domain.Booking) error
	GetBookingByID(bookingID string) (domain.Booking, error)
	UpdateBooking(booking domain.Booking) error
}

// PriceCalculator рассчитывает стоимость проживания.
//
// Это внешний сервис относительно core. Сейчас реализация — SimplePriceCalculator.
type PriceCalculator interface {
	Calculate(room domain.Room, from, to time.Time) (int, error)
}

// Clock даёт текущее время.
//
// В production сюда внедряется SystemClock, в тестах — fixedClock.
type Clock interface {
	Now() time.Time
}

// IDGenerator генерирует идентификаторы брони.
//
// В production используется RandomIDGenerator, в тестах — deterministic generator.
type IDGenerator interface {
	NewID(prefix string) (string, error)
}
