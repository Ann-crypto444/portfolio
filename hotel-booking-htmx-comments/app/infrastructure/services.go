package infrastructure

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hotelbooking/app/domain"
	"time"
)

// Пакет infrastructure содержит конкретные технические реализации портов.
//
// Это внешний слой: его можно менять, не переписывая business logic.

// SystemClock — production-реализация порта Clock.
// В тестах вместо неё можно подставить фиксированное время.
type SystemClock struct{}

// Now возвращает текущее системное время.
func (SystemClock) Now() time.Time { return time.Now() }

// SimplePriceCalculator — простая реализация расчёта стоимости проживания.
type SimplePriceCalculator struct{}

// Calculate считает стоимость как цена за ночь * количество ночей.
func (SimplePriceCalculator) Calculate(room domain.Room, from, to time.Time) (int, error) {
	if err := domain.ValidateStay(domain.Moment(from.Unix()), domain.Moment(to.Unix()), 1); err != nil {
		return 0, err
	}
	nights := int(to.Sub(from).Hours() / 24)
	if nights <= 0 {
		return 0, domain.ValidationError("некорректный период проживания")
	}
	return room.PricePerNight * nights, nil
}

// RandomIDGenerator — production-реализация генератора ID.
type RandomIDGenerator struct{}

// NewID создаёт случайный идентификатор вида booking-1a2b3c4d.
func (RandomIDGenerator) NewID(prefix string) (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("не удалось сгенерировать id: %w", err)
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(buf)), nil
}

// SeedRooms возвращает стартовый набор номеров для in-memory хранилища.
func SeedRooms() []domain.Room {
	return []domain.Room{
		{ID: "room-101", HotelID: "hotel-nevsky", HotelName: "Nevsky Grand", City: "Санкт-Петербург", Address: "Невский пр., 15", Capacity: 2, PricePerNight: 4900, Amenities: []string{"Wi-Fi", "Завтрак", "Кондиционер"}},
		{ID: "room-102", HotelID: "hotel-nevsky", HotelName: "Nevsky Grand", City: "Санкт-Петербург", Address: "Невский пр., 15", Capacity: 3, PricePerNight: 6500, Amenities: []string{"Wi-Fi", "Завтрак", "Вид на город"}},
		{ID: "room-201", HotelID: "hotel-moyka", HotelName: "Moyka Riverside", City: "Санкт-Петербург", Address: "наб. реки Мойки, 44", Capacity: 2, PricePerNight: 7200, Amenities: []string{"Wi-Fi", "SPA", "Фитнес"}},
		{ID: "room-301", HotelID: "hotel-petrograd", HotelName: "Petrograd Boutique", City: "Санкт-Петербург", Address: "Кронверкский пр., 9", Capacity: 4, PricePerNight: 9100, Amenities: []string{"Wi-Fi", "Парковка", "Мини-кухня"}},
		{ID: "room-401", HotelID: "hotel-center", HotelName: "Center Loft", City: "Москва", Address: "Тверская ул., 20", Capacity: 2, PricePerNight: 8400, Amenities: []string{"Wi-Fi", "Завтрак", "Поздний выезд"}},
	}
}
