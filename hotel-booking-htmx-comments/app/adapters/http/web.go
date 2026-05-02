package httpadapter

import (
	"embed"
	"errors"
	"hotelbooking/app/domain"
	"hotelbooking/app/ports"
	"hotelbooking/app/usecases"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Этот пакет — HTTP adapter.
//
// Его задачи:
//   - принять HTTP-запрос;
//   - распарсить входные параметры;
//   - вызвать нужный use case;
//   - превратить результат в HTML-ответ или HTML-фрагмент для HTMX.
//
// Здесь же живут роутер, renderer и view-model'и для шаблонов.

//go:embed templates/**/*.html
var templatesFS embed.FS

// Renderer отвечает только за рендер HTML-шаблонов.
// Он не знает ничего о бизнес-логике.
type Renderer struct{ templates *template.Template }

// NewRenderer читает все шаблоны и регистрирует вспомогательные функции.
func NewRenderer() (*Renderer, error) {
	funcs := template.FuncMap{
		"date":        formatDate,
		"dateTime":    formatDateTime,
		"join":        func(items []string) string { return strings.Join(items, ", ") },
		"statusLabel": statusLabel,
		"statusClass": statusClass,
	}
	tmpl, err := template.New("views").Funcs(funcs).ParseFS(templatesFS, "templates/**/*.html")
	if err != nil {
		return nil, err
	}
	return &Renderer{templates: tmpl}, nil
}

// HTML рендерит указанный шаблон в буфер и пишет его в ResponseWriter.
func (r *Renderer) HTML(w http.ResponseWriter, status int, name string, data any) {
	var b strings.Builder
	if err := r.templates.ExecuteTemplate(&b, name, data); err != nil {
		log.Printf("template render error: %v", err)
		http.Error(w, "template render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}

// isHTMX определяет, был ли запрос инициирован HTMX.
//
// Если да, можно вернуть partial вместо полной HTML-страницы.
func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// UIError — ошибка уже в формате представления.
//
// Это адаптерная модель, а не доменная ошибка.
type UIError struct{ Title, Message string }

// SearchForm хранит значения формы поиска в удобном для шаблона виде.
type SearchForm struct {
	City, From, To string
	Guests         int
	MinPrice       int
	MaxPrice       int
}

// SearchData — данные блока результатов поиска.
type SearchData struct {
	Initial bool
	Form    SearchForm
	Rooms   []usecases.AvailableRoom
	Error   *UIError
}

// GuestForm — данные формы гостя.
type GuestForm struct{ FirstName, SecondName, Phone, Email string }

// BookingPanelData — view-model панели бронирования.
type BookingPanelData struct {
	Initial     bool
	Booking     *domain.Booking
	GuestForm   GuestForm
	Error       *UIError
	HoldExpired bool
	CanConfirm  bool
	CanCancel   bool
}

// HomePageData — данные полной главной страницы.
type HomePageData struct {
	Title        string
	Search       SearchForm
	Results      SearchData
	BookingPanel BookingPanelData
}

// BookingPageData — данные полной страницы карточки брони.
type BookingPageData struct {
	Title string
	Panel BookingPanelData
}

// defaultSearchForm подставляет стартовые значения формы на главной странице.
func defaultSearchForm(now time.Time) SearchForm {
	return SearchForm{City: "Санкт-Петербург", From: date(now.AddDate(0, 0, 1)), To: date(now.AddDate(0, 0, 3)), Guests: 2}
}

// initialResults возвращает начальное пустое состояние блока результатов.
func initialResults(form SearchForm) SearchData { return SearchData{Initial: true, Form: form} }

// initialPanel возвращает начальное пустое состояние панели брони.
func initialPanel() BookingPanelData { return BookingPanelData{Initial: true} }

// panelData преобразует доменную бронь в данные для шаблона.
func panelData(booking domain.Booking, now time.Time, err *UIError, guest GuestForm) BookingPanelData {
	copy := booking
	expired := booking.IsExpired(domain.Moment(now.Unix()))
	return BookingPanelData{Booking: &copy, GuestForm: guest, Error: err, HoldExpired: expired, CanConfirm: booking.Status == domain.BookingStatusHold && !expired, CanCancel: booking.Status != domain.BookingStatusCancelled}
}

// emptyPanel собирает пустую панель, но с ошибкой и заполненной формой.
func emptyPanel(err *UIError, guest GuestForm) BookingPanelData {
	return BookingPanelData{Error: err, GuestForm: guest}
}

// statusLabel переводит внутренний статус в человекочитаемую подпись.
func statusLabel(status domain.BookingStatus) string {
	switch status {
	case domain.BookingStatusHold:
		return "Hold"
	case domain.BookingStatusConfirmed:
		return "Confirmed"
	case domain.BookingStatusCancelled:
		return "Cancelled"
	default:
		return string(status)
	}
}

// statusClass возвращает CSS-класс по статусу брони.
func statusClass(status domain.BookingStatus) string {
	switch status {
	case domain.BookingStatusConfirmed:
		return "ok"
	case domain.BookingStatusCancelled:
		return "bad"
	default:
		return "warn"
	}
}

// formatDate форматирует дату и из time.Time, и из domain.Moment.
func formatDate(value any) string {
	switch v := value.(type) {
	case time.Time:
		return v.Format("2006-01-02")
	case domain.Moment:
		return time.Unix(v.Unix(), 0).UTC().Format("2006-01-02")
	default:
		return ""
	}
}

// formatDateTime форматирует дату-время и из time.Time, и из domain.Moment.
func formatDateTime(value any) string {
	switch v := value.(type) {
	case time.Time:
		return v.Format("2006-01-02 15:04")
	case domain.Moment:
		return time.Unix(v.Unix(), 0).UTC().Format("2006-01-02 15:04")
	default:
		return ""
	}
}

// date — короткий helper для шаблонов и default values.
func date(t time.Time) string { return formatDate(t) }

// uiError маппит внутреннюю доменную ошибку на HTTP-статус и текст для UI.
//
// Это важная часть чистой архитектуры: HTTP-коды определяются здесь,
// а не в use case или domain.
func uiError(err error) (int, UIError) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case domain.ErrValidation:
			return http.StatusBadRequest, UIError{"Ошибка валидации", appErr.Message}
		case domain.ErrRoomNotFound:
			return http.StatusNotFound, UIError{"Номер не найден", appErr.Message}
		case domain.ErrBookingNotFound:
			return http.StatusNotFound, UIError{"Бронь не найдена", appErr.Message}
		case domain.ErrRoomNotAvailable:
			return http.StatusConflict, UIError{"Номер недоступен", appErr.Message}
		case domain.ErrHoldExpired:
			return http.StatusConflict, UIError{"Hold истёк", appErr.Message}
		case domain.ErrStatusConflict:
			return http.StatusConflict, UIError{"Конфликт статуса", appErr.Message}
		}
	}
	return http.StatusInternalServerError, UIError{"Внутренняя ошибка", "сервер не смог обработать запрос"}
}

// parseDate парсит дату из строки HTTP-запроса.
func parseDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, domain.ValidationError("дата обязательна")
	}
	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, domain.ValidationError("дата должна быть в формате YYYY-MM-DD")
	}
	return value, nil
}

// parseInt парсит целое число из формы или query-параметров.
func parseInt(raw, label string, required bool) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return 0, domain.ValidationError(label + " обязательно")
		}
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, domain.ValidationError(label + " должно быть числом")
	}
	return value, nil
}

// searchFormFrom восстанавливает форму поиска из url.Values.
//
// Это позволяет не терять пользовательский ввод в случае ошибки.
func searchFormFrom(values url.Values) SearchForm {
	guests, _ := strconv.Atoi(values.Get("guests"))
	minPrice, _ := strconv.Atoi(values.Get("min_price"))
	maxPrice, _ := strconv.Atoi(values.Get("max_price"))
	if guests == 0 {
		guests = 1
	}
	return SearchForm{City: values.Get("city"), From: values.Get("from"), To: values.Get("to"), Guests: guests, MinPrice: minPrice, MaxPrice: maxPrice}
}

// parseSearch переводит query-параметры в FindAvailableRoomsInput.
func parseSearch(values url.Values) (SearchForm, usecases.FindAvailableRoomsInput, error) {
	form := searchFormFrom(values)
	guests, err := parseInt(values.Get("guests"), "количество гостей", true)
	if err != nil {
		return form, usecases.FindAvailableRoomsInput{}, err
	}
	minPrice, err := parseInt(values.Get("min_price"), "минимальная цена", false)
	if err != nil {
		return form, usecases.FindAvailableRoomsInput{}, err
	}
	maxPrice, err := parseInt(values.Get("max_price"), "максимальная цена", false)
	if err != nil {
		return form, usecases.FindAvailableRoomsInput{}, err
	}
	from, err := parseDate(values.Get("from"))
	if err != nil {
		return form, usecases.FindAvailableRoomsInput{}, err
	}
	to, err := parseDate(values.Get("to"))
	if err != nil {
		return form, usecases.FindAvailableRoomsInput{}, err
	}
	form.Guests, form.MinPrice, form.MaxPrice = guests, minPrice, maxPrice
	return form, usecases.FindAvailableRoomsInput{City: strings.TrimSpace(values.Get("city")), From: from, To: to, Guests: guests, MinPrice: minPrice, MaxPrice: maxPrice}, nil
}

// parseHold переводит данные формы создания временной брони в HoldBookingInput.
func parseHold(values url.Values) (SearchForm, usecases.HoldBookingInput, error) {
	form := searchFormFrom(values)
	from, err := parseDate(values.Get("from"))
	if err != nil {
		return form, usecases.HoldBookingInput{}, err
	}
	to, err := parseDate(values.Get("to"))
	if err != nil {
		return form, usecases.HoldBookingInput{}, err
	}
	guests, err := parseInt(values.Get("guests"), "количество гостей", true)
	if err != nil {
		return form, usecases.HoldBookingInput{}, err
	}
	form.Guests = guests
	return form, usecases.HoldBookingInput{RoomID: strings.TrimSpace(values.Get("room_id")), From: from, To: to, Guests: guests}, nil
}

// parseGuest читает форму гостя для сценария подтверждения.
func parseGuest(values url.Values) GuestForm {
	return GuestForm{FirstName: strings.TrimSpace(values.Get("first_name")), SecondName: strings.TrimSpace(values.Get("second_name")), Phone: strings.TrimSpace(values.Get("phone")), Email: strings.TrimSpace(values.Get("email"))}
}

// PageHandler обслуживает полные страницы верхнего уровня.
//
// Зависимости внедряются из main.go:
//   - renderer отвечает за HTML;
//   - clock нужен для стартовых значений формы.
type PageHandler struct {
	renderer *Renderer
	clock    ports.Clock
}

// NewPageHandler — конструктор handler'а главной страницы.
func NewPageHandler(renderer *Renderer, clock ports.Clock) *PageHandler {
	return &PageHandler{renderer: renderer, clock: clock}
}

// Home обслуживает GET / и показывает стартовую страницу.
func (h *PageHandler) Home(w http.ResponseWriter, _ *http.Request) {
	form := defaultSearchForm(h.clock.Now())
	h.renderer.HTML(w, http.StatusOK, "page_home", HomePageData{Title: "Сервис бронирования отелей", Search: form, Results: initialResults(form), BookingPanel: initialPanel()})
}

// RoomHandler обрабатывает endpoint'ы поиска номеров.
//
// Зависимости внедряются из main.go:
//   - renderer;
//   - findUC.
type RoomHandler struct {
	renderer *Renderer
	findUC   *usecases.FindAvailableRoomsUseCase
}

// NewRoomHandler — конструктор handler'а поиска номеров.
func NewRoomHandler(renderer *Renderer, findUC *usecases.FindAvailableRoomsUseCase) *RoomHandler {
	return &RoomHandler{renderer: renderer, findUC: findUC}
}

// GetAvailableRooms обслуживает GET /rooms/available.
func (h *RoomHandler) GetAvailableRooms(w http.ResponseWriter, r *http.Request) {
	form, input, err := parseSearch(r.URL.Query())
	if err != nil {
		status, ui := uiError(err)
		h.render(w, r, status, form, SearchData{Form: form, Error: &ui})
		return
	}
	rooms, err := h.findUC.Execute(input)
	if err != nil {
		status, ui := uiError(err)
		h.render(w, r, status, form, SearchData{Form: form, Error: &ui})
		return
	}
	h.render(w, r, http.StatusOK, form, SearchData{Form: form, Rooms: rooms})
}

// render выбирает, вернуть partial для HTMX или полную страницу для обычного запроса.
func (h *RoomHandler) render(w http.ResponseWriter, r *http.Request, status int, form SearchForm, results SearchData) {
	if isHTMX(r) {
		h.renderer.HTML(w, status, "partial_search_results", results)
		return
	}
	h.renderer.HTML(w, status, "page_home", HomePageData{Title: "Поиск свободных номеров", Search: form, Results: results, BookingPanel: initialPanel()})
}

// BookingHandler обрабатывает все endpoint'ы, связанные с бронью.
//
// Зависимости внедряются из main.go.
type BookingHandler struct {
	renderer  *Renderer
	holdUC    *usecases.HoldBookingUseCase
	confirmUC *usecases.ConfirmBookingUseCase
	cancelUC  *usecases.CancelBookingUseCase
	getUC     *usecases.GetBookingUseCase
	clock     ports.Clock
}

// NewBookingHandler — конструктор handler'а бронирования.
func NewBookingHandler(renderer *Renderer, holdUC *usecases.HoldBookingUseCase, confirmUC *usecases.ConfirmBookingUseCase, cancelUC *usecases.CancelBookingUseCase, getUC *usecases.GetBookingUseCase, clock ports.Clock) *BookingHandler {
	return &BookingHandler{renderer: renderer, holdUC: holdUC, confirmUC: confirmUC, cancelUC: cancelUC, getUC: getUC, clock: clock}
}

// HoldBooking обслуживает POST /bookings/hold.
func (h *BookingHandler) HoldBooking(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		status, ui := uiError(domain.ValidationError("не удалось прочитать форму"))
		h.renderHoldError(w, r, status, searchFormFrom(r.Form), ui)
		return
	}
	form, input, err := parseHold(r.Form)
	if err != nil {
		status, ui := uiError(err)
		h.renderHoldError(w, r, status, form, ui)
		return
	}
	booking, err := h.holdUC.Execute(input)
	if err != nil {
		status, ui := uiError(err)
		h.renderHoldError(w, r, status, form, ui)
		return
	}
	h.redirectOrSwapBooking(w, r, http.StatusCreated, panelData(booking, h.clock.Now(), nil, GuestForm{}))
}

// ConfirmBooking обслуживает POST /bookings/{id}/confirm.
func (h *BookingHandler) ConfirmBooking(w http.ResponseWriter, r *http.Request) {
	bookingID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		status, ui := uiError(domain.ValidationError("не удалось прочитать форму"))
		h.renderBooking(w, r, status, bookingID, domain.Booking{}, &ui, GuestForm{})
		return
	}
	guest := parseGuest(r.Form)
	booking, err := h.confirmUC.Execute(usecases.ConfirmBookingInput{BookingID: bookingID, FirstName: guest.FirstName, SecondName: guest.SecondName, Phone: guest.Phone, Email: guest.Email})
	if err != nil {
		status, ui := uiError(err)
		h.renderBooking(w, r, status, bookingID, booking, &ui, guest)
		return
	}
	h.redirectOrSwapBooking(w, r, http.StatusOK, panelData(booking, h.clock.Now(), nil, guest))
}

// CancelBooking обслуживает POST /bookings/{id}/cancel.
func (h *BookingHandler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	bookingID := r.PathValue("id")
	booking, err := h.cancelUC.Execute(bookingID)
	if err != nil {
		status, ui := uiError(err)
		h.renderBooking(w, r, status, bookingID, booking, &ui, GuestForm{})
		return
	}
	h.redirectOrSwapBooking(w, r, http.StatusOK, panelData(booking, h.clock.Now(), nil, GuestForm{}))
}

// GetBooking обслуживает GET /bookings/{id}.
func (h *BookingHandler) GetBooking(w http.ResponseWriter, r *http.Request) {
	bookingID := r.PathValue("id")
	booking, err := h.getUC.Execute(bookingID)
	if err != nil {
		status, ui := uiError(err)
		h.renderBooking(w, r, status, bookingID, domain.Booking{}, &ui, GuestForm{})
		return
	}
	panel := panelData(booking, h.clock.Now(), nil, GuestForm{})
	if isHTMX(r) {
		h.renderer.HTML(w, http.StatusOK, "partial_booking_panel", panel)
		return
	}
	h.renderer.HTML(w, http.StatusOK, "page_booking", BookingPageData{Title: "Бронь " + booking.ID, Panel: panel})
}

// OpenBooking — вспомогательный endpoint для открытия карточки по ID из формы.
func (h *BookingHandler) OpenBooking(w http.ResponseWriter, r *http.Request) {
	bookingID := strings.TrimSpace(r.URL.Query().Get("booking_id"))
	if bookingID == "" {
		form := defaultSearchForm(h.clock.Now())
		ui := UIError{Title: "Нужен ID брони", Message: "Укажи booking_id, например booking-c7499c78."}
		h.renderer.HTML(w, http.StatusBadRequest, "page_home", HomePageData{Title: "Открыть карточку брони", Search: form, Results: initialResults(form), BookingPanel: emptyPanel(&ui, GuestForm{})})
		return
	}
	http.Redirect(w, r, "/bookings/"+bookingID, http.StatusSeeOther)
}

// renderHoldError рендерит ошибку создания временной брони.
func (h *BookingHandler) renderHoldError(w http.ResponseWriter, r *http.Request, status int, form SearchForm, ui UIError) {
	panel := emptyPanel(&ui, GuestForm{})
	if isHTMX(r) {
		h.renderer.HTML(w, status, "partial_booking_panel", panel)
		return
	}
	h.renderer.HTML(w, status, "page_home", HomePageData{Title: "Ошибка создания временной брони", Search: form, Results: initialResults(form), BookingPanel: panel})
}

// renderBooking рендерит либо ошибку, либо существующую карточку брони.
func (h *BookingHandler) renderBooking(w http.ResponseWriter, r *http.Request, status int, bookingID string, booking domain.Booking, ui *UIError, guest GuestForm) {
	panel := emptyPanel(ui, guest)
	if booking.ID != "" {
		panel = panelData(booking, h.clock.Now(), ui, guest)
	}
	if isHTMX(r) {
		h.renderer.HTML(w, status, "partial_booking_panel", panel)
		return
	}
	title := "Ошибка чтения брони"
	if booking.ID != "" {
		title = "Бронь " + booking.ID
	} else if bookingID != "" {
		title = "Бронь " + bookingID
	}
	h.renderer.HTML(w, status, "page_booking", BookingPageData{Title: title, Panel: panel})
}

// redirectOrSwapBooking выбирает поведение после успешной операции над бронью:
//   - для HTMX вернуть partial и обновить URL через HX-Push-Url;
//   - для обычного HTTP сделать redirect на /bookings/{id}.
func (h *BookingHandler) redirectOrSwapBooking(w http.ResponseWriter, r *http.Request, status int, panel BookingPanelData) {
	if panel.Booking == nil {
		h.renderer.HTML(w, status, "partial_booking_panel", panel)
		return
	}
	path := "/bookings/" + panel.Booking.ID
	if isHTMX(r) {
		w.Header().Set("HX-Push-Url", path)
		h.renderer.HTML(w, status, "partial_booking_panel", panel)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// NewRouter регистрирует все HTTP-endpoint'ы приложения.
func NewRouter(pageHandler *PageHandler, roomHandler *RoomHandler, bookingHandler *BookingHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", pageHandler.Home)
	mux.HandleFunc("GET /rooms/available", roomHandler.GetAvailableRooms)
	mux.HandleFunc("POST /bookings/hold", bookingHandler.HoldBooking)
	mux.HandleFunc("GET /bookings/open", bookingHandler.OpenBooking)
	mux.HandleFunc("GET /bookings/{id}", bookingHandler.GetBooking)
	mux.HandleFunc("POST /bookings/{id}/confirm", bookingHandler.ConfirmBooking)
	mux.HandleFunc("POST /bookings/{id}/cancel", bookingHandler.CancelBooking)
	return mux
}
