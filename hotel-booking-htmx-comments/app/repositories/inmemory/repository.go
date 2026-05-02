package inmemory

import (
	"hotelbooking/app/domain"
	"sync"
)

// Repository — in-memory реализация сразу нескольких портов.
//
// Один и тот же объект repo в main.go используется как:
//   - AvailabilityReader
//   - AvailabilityChecker
//   - RoomReader
//   - BookingRepository
//
// Это удобно для учебного проекта и хорошо показывает инверсию зависимостей.
type Repository struct {
	// mu защищает карты, потому что HTTP-запросы обрабатываются параллельно.
	mu sync.RWMutex
	// rooms — каталог номеров.
	rooms map[string]domain.Room
	// bookings — текущее состояние броней.
	bookings map[string]domain.Booking
}

// NewRepository создаёт in-memory repository и заполняет его начальными номерами.
func NewRepository(rooms []domain.Room) *Repository {
	roomMap := make(map[string]domain.Room, len(rooms))
	for _, room := range rooms {
		roomMap[room.ID] = copyRoom(room)
	}
	return &Repository{rooms: roomMap, bookings: make(map[string]domain.Booking)}
}

// FindAvailableRooms реализует порт AvailabilityReader.
func (r *Repository) FindAvailableRooms(filter domain.RoomSearchFilter, now domain.Moment) ([]domain.Room, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]domain.Room, 0, len(r.rooms))
	for _, room := range r.rooms {
		if filter.City != "" && room.City != filter.City {
			continue
		}
		if room.Capacity < filter.Guests {
			continue
		}
		if !r.isRoomAvailableLocked(room.ID, filter.From, filter.To, now) {
			continue
		}
		result = append(result, copyRoom(room))
	}
	return result, nil
}

// IsRoomAvailable реализует порт AvailabilityChecker.
func (r *Repository) IsRoomAvailable(roomID string, from, to, now domain.Moment) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.rooms[roomID]; !ok {
		return false, domain.RoomNotFoundError(roomID)
	}
	return r.isRoomAvailableLocked(roomID, from, to, now), nil
}

// GetRoomByID реализует порт RoomReader.
func (r *Repository) GetRoomByID(roomID string) (domain.Room, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	room, ok := r.rooms[roomID]
	if !ok {
		return domain.Room{}, domain.RoomNotFoundError(roomID)
	}
	return copyRoom(room), nil
}

// CreateBooking реализует запись новой брони в хранилище.
func (r *Repository) CreateBooking(booking domain.Booking) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bookings[booking.ID] = copyBooking(booking)
	return nil
}

// GetBookingByID читает бронь по ID.
func (r *Repository) GetBookingByID(bookingID string) (domain.Booking, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	booking, ok := r.bookings[bookingID]
	if !ok {
		return domain.Booking{}, domain.BookingNotFoundError(bookingID)
	}
	return copyBooking(booking), nil
}

// UpdateBooking перезаписывает существующую бронь.
func (r *Repository) UpdateBooking(booking domain.Booking) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bookings[booking.ID]; !ok {
		return domain.BookingNotFoundError(booking.ID)
	}
	r.bookings[booking.ID] = copyBooking(booking)
	return nil
}

// isRoomAvailableLocked — внутренняя функция, которая предполагает,
// что lock уже взят снаружи.
func (r *Repository) isRoomAvailableLocked(roomID string, from, to, now domain.Moment) bool {
	for _, booking := range r.bookings {
		if booking.Room.ID != roomID {
			continue
		}
		if !blocksRoom(booking, now) {
			continue
		}
		if overlaps(booking.From, booking.To, from, to) {
			return false
		}
	}
	return true
}

// blocksRoom отвечает на вопрос: должна ли бронь сейчас блокировать номер?
func blocksRoom(booking domain.Booking, now domain.Moment) bool {
	switch booking.Status {
	case domain.BookingStatusCancelled:
		return false
	case domain.BookingStatusHold:
		return !booking.IsExpired(now)
	case domain.BookingStatusConfirmed:
		return true
	default:
		return false
	}
}

// overlaps проверяет пересечение двух периодов проживания.
func overlaps(aFrom, aTo, bFrom, bTo domain.Moment) bool {
	return aFrom.Before(bTo) && bFrom.Before(aTo)
}

// copyRoom делает защитную копию номера.
func copyRoom(room domain.Room) domain.Room {
	room.Amenities = append([]string(nil), room.Amenities...)
	return room
}

// copyBooking делает защитную копию брони и вложенного гостя.
func copyBooking(booking domain.Booking) domain.Booking {
	booking.Room.Amenities = append([]string(nil), booking.Room.Amenities...)
	if booking.Guest != nil {
		guestCopy := *booking.Guest
		booking.Guest = &guestCopy
	}
	return booking
}
