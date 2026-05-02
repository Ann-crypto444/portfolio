package usecases

import (
	"hotelbooking/app/domain"
	"hotelbooking/app/ports"
	"time"
)

// toMoment переводит внешнее time.Time во внутренний domain.Moment.
//
// Это граница между слоями: domain не зависит от пакета time.
func toMoment(value time.Time) domain.Moment { return domain.Moment(value.Unix()) }

// ===== Search rooms =====

// FindAvailableRoomsInput — входные данные сценария поиска свободных номеров.
type FindAvailableRoomsInput struct {
	City     string
	From     time.Time
	To       time.Time
	Guests   int
	MinPrice int
	MaxPrice int
}

// AvailableRoom — результат поиска для UI-слоя.
type AvailableRoom struct {
	Room       domain.Room
	PriceTotal int
}

// FindAvailableRoomsUseCase — сценарий "найти свободные номера".
//
// Зависимости внедряются из main.go:
//   - availability: источник номеров;
//   - prices: калькулятор стоимости;
//   - clock: источник текущего времени.
type FindAvailableRoomsUseCase struct {
	availability ports.AvailabilityReader
	prices       ports.PriceCalculator
	clock        ports.Clock
}

// NewFindAvailableRoomsUseCase собирает use case поиска.
func NewFindAvailableRoomsUseCase(availability ports.AvailabilityReader, prices ports.PriceCalculator, clock ports.Clock) *FindAvailableRoomsUseCase {
	return &FindAvailableRoomsUseCase{availability: availability, prices: prices, clock: clock}
}

// Execute выполняет сценарий поиска:
//  1. валидирует вход;
//  2. запрашивает свободные номера через порт;
//  3. рассчитывает цену;
//  4. фильтрует по диапазону цены.
func (uc *FindAvailableRoomsUseCase) Execute(input FindAvailableRoomsInput) ([]AvailableRoom, error) {
	from := toMoment(input.From)
	to := toMoment(input.To)
	if err := domain.ValidateStay(from, to, input.Guests); err != nil {
		return nil, err
	}
	if input.MinPrice < 0 || input.MaxPrice < 0 {
		return nil, domain.ValidationError("цена не может быть отрицательной")
	}
	if input.MaxPrice > 0 && input.MinPrice > input.MaxPrice {
		return nil, domain.ValidationError("минимальная цена не может быть больше максимальной")
	}

	rooms, err := uc.availability.FindAvailableRooms(domain.RoomSearchFilter{
		City:   input.City,
		From:   from,
		To:     to,
		Guests: input.Guests,
	}, toMoment(uc.clock.Now()))
	if err != nil {
		return nil, err
	}

	result := make([]AvailableRoom, 0, len(rooms))
	for _, room := range rooms {
		priceTotal, err := uc.prices.Calculate(room, input.From, input.To)
		if err != nil {
			return nil, err
		}
		if input.MinPrice > 0 && priceTotal < input.MinPrice {
			continue
		}
		if input.MaxPrice > 0 && priceTotal > input.MaxPrice {
			continue
		}
		result = append(result, AvailableRoom{Room: room, PriceTotal: priceTotal})
	}

	return result, nil
}

// ===== Hold booking =====

// HoldBookingInput — входные данные сценария создания временной брони.
type HoldBookingInput struct {
	RoomID string
	From   time.Time
	To     time.Time
	Guests int
}

// HoldBookingUseCase — сценарий "создать временную бронь (hold)".
//
// Зависимости внедряются из main.go:
//   - rooms читает номер по ID;
//   - availability проверяет доступность;
//   - bookings сохраняет бронь;
//   - prices считает итоговую цену;
//   - clock даёт текущее время;
//   - ids генерирует booking ID.
type HoldBookingUseCase struct {
	rooms        ports.RoomReader
	availability ports.AvailabilityChecker
	bookings     ports.BookingRepository
	prices       ports.PriceCalculator
	clock        ports.Clock
	ids          ports.IDGenerator
}

// NewHoldBookingUseCase собирает use case создания hold-брони.
func NewHoldBookingUseCase(rooms ports.RoomReader, availability ports.AvailabilityChecker, bookings ports.BookingRepository, prices ports.PriceCalculator, clock ports.Clock, ids ports.IDGenerator) *HoldBookingUseCase {
	return &HoldBookingUseCase{rooms: rooms, availability: availability, bookings: bookings, prices: prices, clock: clock, ids: ids}
}

// Execute выполняет сценарий создания hold-брони.
func (uc *HoldBookingUseCase) Execute(input HoldBookingInput) (domain.Booking, error) {
	from := toMoment(input.From)
	to := toMoment(input.To)
	if err := domain.ValidateStay(from, to, input.Guests); err != nil {
		return domain.Booking{}, err
	}
	room, err := uc.rooms.GetRoomByID(input.RoomID)
	if err != nil {
		return domain.Booking{}, err
	}
	now := toMoment(uc.clock.Now())
	available, err := uc.availability.IsRoomAvailable(input.RoomID, from, to, now)
	if err != nil {
		return domain.Booking{}, err
	}
	if !available {
		return domain.Booking{}, domain.RoomNotAvailableError(input.RoomID)
	}
	priceTotal, err := uc.prices.Calculate(room, input.From, input.To)
	if err != nil {
		return domain.Booking{}, err
	}
	bookingID, err := uc.ids.NewID("booking")
	if err != nil {
		return domain.Booking{}, err
	}
	booking, err := domain.NewHoldBooking(
		bookingID,
		room.Snapshot(),
		from,
		to,
		input.Guests,
		priceTotal,
		now.AddSeconds(domain.HoldTTLSeconds),
	)
	if err != nil {
		return domain.Booking{}, err
	}
	if err := uc.bookings.CreateBooking(booking); err != nil {
		return domain.Booking{}, err
	}
	return booking, nil
}

// ===== Confirm booking =====

// ConfirmBookingInput — входные данные для подтверждения брони.
type ConfirmBookingInput struct {
	BookingID  string
	FirstName  string
	SecondName string
	Phone      string
	Email      string
}

// ConfirmBookingUseCase — сценарий подтверждения брони.
//
// Ему нужны только репозиторий броней и источник времени.
type ConfirmBookingUseCase struct {
	bookings ports.BookingRepository
	clock    ports.Clock
}

// NewConfirmBookingUseCase собирает use case подтверждения.
func NewConfirmBookingUseCase(bookings ports.BookingRepository, clock ports.Clock) *ConfirmBookingUseCase {
	return &ConfirmBookingUseCase{bookings: bookings, clock: clock}
}

// Execute выполняет подтверждение брони.
func (uc *ConfirmBookingUseCase) Execute(input ConfirmBookingInput) (domain.Booking, error) {
	booking, err := uc.bookings.GetBookingByID(input.BookingID)
	if err != nil {
		return domain.Booking{}, err
	}
	guest := domain.Guest{
		FirstName:  input.FirstName,
		SecondName: input.SecondName,
		Phone:      input.Phone,
		Email:      input.Email,
	}
	if err := booking.Confirm(guest, toMoment(uc.clock.Now())); err != nil {
		// Возвращаем текущую бронь вместе с ошибкой, чтобы adapter мог показать её в UI.
		return booking, err
	}
	if err := uc.bookings.UpdateBooking(booking); err != nil {
		return booking, err
	}
	return booking, nil
}

// ===== Cancel booking =====

// CancelBookingUseCase — сценарий отмены брони.
type CancelBookingUseCase struct{ bookings ports.BookingRepository }

// NewCancelBookingUseCase собирает use case отмены.
func NewCancelBookingUseCase(bookings ports.BookingRepository) *CancelBookingUseCase {
	return &CancelBookingUseCase{bookings: bookings}
}

// Execute отменяет бронь и сохраняет новое состояние.
func (uc *CancelBookingUseCase) Execute(bookingID string) (domain.Booking, error) {
	booking, err := uc.bookings.GetBookingByID(bookingID)
	if err != nil {
		return domain.Booking{}, err
	}
	if err := booking.Cancel(); err != nil {
		return booking, err
	}
	if err := uc.bookings.UpdateBooking(booking); err != nil {
		return booking, err
	}
	return booking, nil
}

// ===== Get booking =====

// GetBookingUseCase — сценарий чтения карточки брони по ID.
type GetBookingUseCase struct{ bookings ports.BookingRepository }

// NewGetBookingUseCase собирает use case чтения брони.
func NewGetBookingUseCase(bookings ports.BookingRepository) *GetBookingUseCase {
	return &GetBookingUseCase{bookings: bookings}
}

// Execute просто читает бронь из репозитория.
func (uc *GetBookingUseCase) Execute(bookingID string) (domain.Booking, error) {
	return uc.bookings.GetBookingByID(bookingID)
}
