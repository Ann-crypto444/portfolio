package domain

// HoldTTLSeconds задаёт время жизни hold-брони в секундах.
//
// Это доменное правило: временная бронь живёт ограниченное время,
// поэтому константа находится во внутреннем слое, а не в HTTP или инфраструктуре.
const HoldTTLSeconds int64 = 15 * 60

// ErrorCode — внутренний код ошибки приложения.
//
// Идея такая: domain и use cases не знают ничего про HTTP-коды.
// Они возвращают только внутреннюю ошибку, а уже адаптер решает,
// превратить её в 400 / 404 / 409 / 500.
type ErrorCode string

const (
	ErrValidation       ErrorCode = "validation_error"
	ErrRoomNotFound     ErrorCode = "room_not_found"
	ErrRoomNotAvailable ErrorCode = "room_not_available"
	ErrBookingNotFound  ErrorCode = "booking_not_found"
	ErrHoldExpired      ErrorCode = "hold_expired"
	ErrStatusConflict   ErrorCode = "status_conflict"
)

// AppError — доменная ошибка, независимая от транспорта и инфраструктуры.
type AppError struct {
	Code    ErrorCode
	Message string
}

// Error реализует стандартный интерфейс error.
func (e *AppError) Error() string { return e.Message }

// ValidationError создаёт ошибку нарушения бизнес-правила.
func ValidationError(message string) error {
	return &AppError{Code: ErrValidation, Message: message}
}

// RoomNotFoundError создаёт ошибку "номер не найден".
func RoomNotFoundError(roomID string) error {
	return &AppError{Code: ErrRoomNotFound, Message: "номер " + roomID + " не найден"}
}

// RoomNotAvailableError создаёт ошибку "номер уже занят".
func RoomNotAvailableError(roomID string) error {
	return &AppError{Code: ErrRoomNotAvailable, Message: "номер " + roomID + " уже занят на выбранные даты"}
}

// BookingNotFoundError создаёт ошибку "бронь не найдена".
func BookingNotFoundError(bookingID string) error {
	return &AppError{Code: ErrBookingNotFound, Message: "бронь " + bookingID + " не найдена"}
}

// HoldExpiredError создаёт ошибку истечения hold-брони.
func HoldExpiredError(bookingID string) error {
	return &AppError{Code: ErrHoldExpired, Message: "hold по брони " + bookingID + " истёк, нужно оформить заново"}
}

// StatusConflictError создаёт ошибку конфликта статуса.
func StatusConflictError(message string) error {
	return &AppError{Code: ErrStatusConflict, Message: message}
}

// Moment — собственный доменный тип времени.
//
// Он хранит unix-время в секундах. Благодаря этому domain полностью
// не зависит даже от пакета time.
type Moment int64

// Unix возвращает внутреннее значение момента времени.
func (m Moment) Unix() int64 { return int64(m) }

// AddSeconds прибавляет к моменту заданное число секунд.
func (m Moment) AddSeconds(seconds int64) Moment { return m + Moment(seconds) }

// Before проверяет, что текущий момент раньше другого.
func (m Moment) Before(other Moment) bool { return m < other }

// After проверяет, что текущий момент позже другого.
func (m Moment) After(other Moment) bool { return m > other }

// isBlank — локальный helper для проверки пустой строки без strings.TrimSpace.
//
// Он нужен только затем, чтобы domain не импортировал ничего вообще.
func isBlank(value string) bool {
	if len(value) == 0 {
		return true
	}
	for _, r := range value {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

// Room — сущность номера в каталоге.
type Room struct {
	ID            string
	HotelID       string
	HotelName     string
	City          string
	Address       string
	Capacity      int
	PricePerNight int
	Amenities     []string
}

// RoomSnapshot — слепок номера на момент создания брони.
//
// Он встраивается в Booking, чтобы бронь сохраняла снимок данных,
// даже если каталог номеров потом изменится.
type RoomSnapshot struct {
	ID            string
	HotelID       string
	HotelName     string
	City          string
	Address       string
	Capacity      int
	PricePerNight int
	Amenities     []string
}

// Snapshot строит слепок номера для встраивания в бронь.
func (r Room) Snapshot() RoomSnapshot {
	amenities := append([]string(nil), r.Amenities...)
	return RoomSnapshot{
		ID:            r.ID,
		HotelID:       r.HotelID,
		HotelName:     r.HotelName,
		City:          r.City,
		Address:       r.Address,
		Capacity:      r.Capacity,
		PricePerNight: r.PricePerNight,
		Amenities:     amenities,
	}
}

// RoomSearchFilter — фильтр доменного поиска свободных номеров.
type RoomSearchFilter struct {
	City   string
	From   Moment
	To     Moment
	Guests int
}

// Guest — данные гостя, которые появляются на этапе подтверждения брони.
type Guest struct {
	FirstName  string
	SecondName string
	Phone      string
	Email      string
}

// Validate проверяет минимальные требования к заполнению данных гостя.
func (g Guest) Validate() error {
	if isBlank(g.FirstName) {
		return ValidationError("имя гостя обязательно")
	}
	if isBlank(g.SecondName) {
		return ValidationError("фамилия гостя обязательна")
	}
	if isBlank(g.Phone) {
		return ValidationError("телефон обязателен")
	}
	if isBlank(g.Email) {
		return ValidationError("email обязателен")
	}
	return nil
}

// BookingStatus описывает жизненный цикл брони.
type BookingStatus string

const (
	BookingStatusHold      BookingStatus = "hold"
	BookingStatusConfirmed BookingStatus = "confirmed"
	BookingStatusCancelled BookingStatus = "cancelled"
)

// Booking — ключевая доменная сущность бронирования.
type Booking struct {
	ID         string
	Room       RoomSnapshot
	From       Moment
	To         Moment
	Guests     int
	PriceTotal int
	Status     BookingStatus
	ExpiresAt  Moment
	Guest      *Guest
}

// ValidateStay проверяет доменные правила для периода проживания.
func ValidateStay(from, to Moment, guests int) error {
	if !to.After(from) {
		return ValidationError("дата выезда должна быть позже даты заезда")
	}
	if guests < 1 {
		return ValidationError("количество гостей должно быть не меньше 1")
	}
	return nil
}

// NewHoldBooking создаёт новую hold-бронь и валидирует инварианты.
func NewHoldBooking(id string, room RoomSnapshot, from, to Moment, guests, priceTotal int, expiresAt Moment) (Booking, error) {
	if err := ValidateStay(from, to, guests); err != nil {
		return Booking{}, err
	}
	if id == "" {
		return Booking{}, ValidationError("id брони не может быть пустым")
	}
	if priceTotal <= 0 {
		return Booking{}, ValidationError("цена за период должна быть больше 0")
	}
	return Booking{
		ID:         id,
		Room:       room,
		From:       from,
		To:         to,
		Guests:     guests,
		PriceTotal: priceTotal,
		Status:     BookingStatusHold,
		ExpiresAt:  expiresAt,
	}, nil
}

// IsExpired определяет, истёк ли hold на текущий момент времени.
func (b Booking) IsExpired(now Moment) bool {
	return b.Status == BookingStatusHold && !now.Before(b.ExpiresAt)
}

// Confirm переводит бронь из hold в confirmed.
//
// Здесь находится именно бизнес-правило, а не транспортная логика.
func (b *Booking) Confirm(guest Guest, now Moment) error {
	if b.Status != BookingStatusHold {
		return StatusConflictError("подтвердить можно только бронь со статусом hold")
	}
	if b.IsExpired(now) {
		return HoldExpiredError(b.ID)
	}
	if err := guest.Validate(); err != nil {
		return err
	}
	copyGuest := guest
	b.Guest = &copyGuest
	b.Status = BookingStatusConfirmed
	return nil
}

// Cancel отменяет бронь.
func (b *Booking) Cancel() error {
	if b.Status == BookingStatusCancelled {
		return StatusConflictError("бронь уже отменена")
	}
	b.Status = BookingStatusCancelled
	return nil
}
