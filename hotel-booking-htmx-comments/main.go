package main

import (
	httpadapter "hotelbooking/app/adapters/http"
	"hotelbooking/app/infrastructure"
	"hotelbooking/app/repositories/inmemory"
	"hotelbooking/app/usecases"
	"log"
	"net/http"
	"time"
)

// main — composition root приложения.

func main() {
	// Создание concrete implementations инфраструктурного слоя.
	clock := infrastructure.SystemClock{}
	ids := infrastructure.RandomIDGenerator{}
	prices := infrastructure.SimplePriceCalculator{}
	repo := inmemory.NewRepository(infrastructure.SeedRooms())

	// Создание renderer для HTML-шаблонов.
	renderer, err := httpadapter.NewRenderer()
	if err != nil {
		log.Fatalf("renderer init failed: %v", err)
	}

	// Сборка use case'ов.
	//
	// Обрати внимание: один и тот же repo передаётся под разными интерфейсами.
	findRoomsUC := usecases.NewFindAvailableRoomsUseCase(repo, prices, clock)
	holdBookingUC := usecases.NewHoldBookingUseCase(repo, repo, repo, prices, clock, ids)
	confirmBookingUC := usecases.NewConfirmBookingUseCase(repo, clock)
	cancelBookingUC := usecases.NewCancelBookingUseCase(repo)
	getBookingUC := usecases.NewGetBookingUseCase(repo)

	// Сборка HTTP adapter'ов.
	pageHandler := httpadapter.NewPageHandler(renderer, clock)
	roomHandler := httpadapter.NewRoomHandler(renderer, findRoomsUC)
	bookingHandler := httpadapter.NewBookingHandler(renderer, holdBookingUC, confirmBookingUC, cancelBookingUC, getBookingUC, clock)

	// Создание HTTP-сервера.
	server := &http.Server{
		Addr:              ":8080",
		Handler:           loggingMiddleware(httpadapter.NewRouter(pageHandler, roomHandler, bookingHandler)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println("hotel-booking-htmx started at http://localhost:8080")
	log.Fatal(server.ListenAndServe())
}

// loggingMiddleware — технический middleware для логирования входящих запросов.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.String())
		next.ServeHTTP(w, r)
	})
}
