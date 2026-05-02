package main

import (
	httpadapter "hotelbooking/app/adapters/http"
	"hotelbooking/app/infrastructure"
	"hotelbooking/app/repositories/inmemory"
	"hotelbooking/app/usecases"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fixedClock — тестовая реализация порта Clock.
type fixedClock struct{ now time.Time }

// Now возвращает заранее зафиксированное время.
func (c fixedClock) Now() time.Time { return c.now }

// sequentialIDGenerator — тестовый генератор ID с предсказуемыми значениями.
type sequentialIDGenerator struct{ next int }

// NewID возвращает детерминированный ID, чтобы интеграционный тест был стабильным.
func (g *sequentialIDGenerator) NewID(prefix string) (string, error) {
	g.next++
	return prefix + "-test-" + time.Unix(int64(g.next), 0).UTC().Format("150405"), nil
}

// TestFullBookingWorkflow проверяет весь пользовательский сценарий целиком:
// поиск номера -> hold -> confirm -> get booking -> cancel.
//
// Чтобы увидеть пошаговый вывод успешных шагов, запускай:
//
//	go test -v -run TestFullBookingWorkflow
func TestFullBookingWorkflow(t *testing.T) {
	handler := newTestHandler(t)

	var bookingLocation string
	var bookingID string

	t.Run("step_01_search_available_rooms", func(t *testing.T) {
		t.Log("Шаг 1. Ищем свободные номера по городу, датам и числу гостей.")

		searchReq := httptest.NewRequest(
			http.MethodGet,
			"/rooms/available?city=%D0%A1%D0%B0%D0%BD%D0%BA%D1%82-%D0%9F%D0%B5%D1%82%D0%B5%D1%80%D0%B1%D1%83%D1%80%D0%B3&from=2026-06-10&to=2026-06-12&guests=2",
			nil,
		)
		searchRec := httptest.NewRecorder()
		handler.ServeHTTP(searchRec, searchReq)

		assertStatus(t, searchRec.Result(), http.StatusOK)
		body := mustReadBody(t, searchRec.Result().Body)
		assertStringContains(t, body, "Nevsky Grand")
		t.Logf("OK: поиск вернул HTTP %d и в выдаче найден отель Nevsky Grand.", http.StatusOK)
	})

	t.Run("step_02_create_hold", func(t *testing.T) {
		t.Log("Шаг 2. Создаём временную бронь (hold) для выбранного номера.")

		holdForm := url.Values{}
		holdForm.Set("room_id", "room-101")
		holdForm.Set("from", "2026-06-10")
		holdForm.Set("to", "2026-06-12")
		holdForm.Set("guests", "2")
		holdForm.Set("city", "Санкт-Петербург")

		bookingLocation = postAndReadLocation(t, handler, "/bookings/hold", holdForm)
		if !strings.HasPrefix(bookingLocation, "/bookings/") {
			t.Fatalf("unexpected location after hold: %s", bookingLocation)
		}
		bookingID = strings.TrimPrefix(bookingLocation, "/bookings/")
		t.Logf("OK: создан hold, bookingID=%s, redirect=%s.", bookingID, bookingLocation)
	})

	t.Run("step_03_confirm_booking", func(t *testing.T) {
		if bookingID == "" {
			t.Fatal("bookingID is empty; previous step did not complete")
		}
		t.Log("Шаг 3. Подтверждаем hold корректными данными гостя.")

		confirmForm := url.Values{}
		confirmForm.Set("first_name", "Анна")
		confirmForm.Set("second_name", "Беседина")
		confirmForm.Set("phone", "+79990001122")
		confirmForm.Set("email", "anna@example.com")

		confirmLocation := postAndReadLocation(t, handler, "/bookings/"+bookingID+"/confirm", confirmForm)
		if confirmLocation != bookingLocation {
			t.Fatalf("unexpected location after confirm: got %s want %s", confirmLocation, bookingLocation)
		}
		t.Logf("OK: бронь %s подтверждена, redirect=%s.", bookingID, confirmLocation)
	})

	t.Run("step_04_get_confirmed_booking", func(t *testing.T) {
		if bookingLocation == "" {
			t.Fatal("bookingLocation is empty; previous steps did not complete")
		}
		t.Log("Шаг 4. Читаем карточку подтверждённой брони по ID.")

		getReq := httptest.NewRequest(http.MethodGet, bookingLocation, nil)
		getRec := httptest.NewRecorder()
		handler.ServeHTTP(getRec, getReq)

		assertStatus(t, getRec.Result(), http.StatusOK)
		body := mustReadBody(t, getRec.Result().Body)
		assertStringContains(t, body, "Confirmed")
		assertStringContains(t, body, "Анна Беседина")
		t.Logf("OK: карточка брони %s доступна и содержит статус Confirmed и имя гостя.", bookingID)
	})

	t.Run("step_05_cancel_booking", func(t *testing.T) {
		if bookingID == "" {
			t.Fatal("bookingID is empty; previous steps did not complete")
		}
		t.Log("Шаг 5. Отменяем существующую бронь.")

		cancelLocation := postAndReadLocation(t, handler, "/bookings/"+bookingID+"/cancel", url.Values{})
		if cancelLocation != bookingLocation {
			t.Fatalf("unexpected location after cancel: got %s want %s", cancelLocation, bookingLocation)
		}
		t.Logf("OK: бронь %s отменена, redirect=%s.", bookingID, cancelLocation)
	})

	t.Run("step_06_get_cancelled_booking", func(t *testing.T) {
		if bookingLocation == "" {
			t.Fatal("bookingLocation is empty; previous steps did not complete")
		}
		t.Log("Шаг 6. Повторно читаем карточку и проверяем финальный статус Cancelled.")

		getAfterCancelReq := httptest.NewRequest(http.MethodGet, bookingLocation, nil)
		getAfterCancelRec := httptest.NewRecorder()
		handler.ServeHTTP(getAfterCancelRec, getAfterCancelReq)

		assertStatus(t, getAfterCancelRec.Result(), http.StatusOK)
		body := mustReadBody(t, getAfterCancelRec.Result().Body)
		assertStringContains(t, body, "Cancelled")
		t.Logf("OK: финальный статус брони %s = Cancelled.", bookingID)
	})

	t.Logf("Workflow completed successfully: bookingID=%s, finalPath=%s.", bookingID, bookingLocation)
}

// newTestHandler собирает тот же граф зависимостей, что и main.go,
// но с тестовыми реализациями часов и генератора ID.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	clock := fixedClock{now: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
	ids := &sequentialIDGenerator{}
	prices := infrastructure.SimplePriceCalculator{}
	repo := inmemory.NewRepository(infrastructure.SeedRooms())
	renderer, err := httpadapter.NewRenderer()
	if err != nil {
		t.Fatalf("renderer init failed: %v", err)
	}
	findRoomsUC := usecases.NewFindAvailableRoomsUseCase(repo, prices, clock)
	holdBookingUC := usecases.NewHoldBookingUseCase(repo, repo, repo, prices, clock, ids)
	confirmBookingUC := usecases.NewConfirmBookingUseCase(repo, clock)
	cancelBookingUC := usecases.NewCancelBookingUseCase(repo)
	getBookingUC := usecases.NewGetBookingUseCase(repo)
	pageHandler := httpadapter.NewPageHandler(renderer, clock)
	roomHandler := httpadapter.NewRoomHandler(renderer, findRoomsUC)
	bookingHandler := httpadapter.NewBookingHandler(renderer, holdBookingUC, confirmBookingUC, cancelBookingUC, getBookingUC, clock)
	return httpadapter.NewRouter(pageHandler, roomHandler, bookingHandler)
}

// postAndReadLocation выполняет POST-запрос и возвращает заголовок Location.
func postAndReadLocation(t *testing.T, handler http.Handler, path string, form url.Values) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec.Result(), http.StatusSeeOther)
	location := rec.Result().Header.Get("Location")
	if location == "" {
		t.Fatal("expected redirect location header")
	}
	return location
}

// assertStatus проверяет HTTP-статус ответа.
func assertStatus(t *testing.T, res *http.Response, want int) {
	t.Helper()
	if res.StatusCode != want {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status: got %d want %d body=%s", res.StatusCode, want, string(body))
	}
}

// mustReadBody читает тело ответа целиком.
func mustReadBody(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return string(content)
}

// assertStringContains проверяет наличие подстроки в ответе.
func assertStringContains(t *testing.T, content string, substring string) {
	t.Helper()
	if !strings.Contains(content, substring) {
		t.Fatalf("body does not contain %q\nbody:\n%s", substring, content)
	}
}
