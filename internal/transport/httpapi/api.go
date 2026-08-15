// Package httpapi — HTTP-транспорт поверх сценариев приложения.
//
// Слой ведущий, а не ведомый: он вызывает app, а не реализует его порты,
// поэтому и живёт не в internal/infra. Зависимости по-прежнему направлены
// внутрь: cmd → transport → app → todo.
//
// Транспорт тупой по устройству. Он разбирает JSON в команды из сырых строк,
// зовёт ровно один сценарий и переводит его ошибку в код ответа. Доменных
// типов он не знает и знать не должен: превращение строк в значимые объекты —
// работа app, и переносить её сюда нельзя.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// ownerHeader — заголовок, из которого берётся владелец запроса.
//
// Аутентификации пока нет, и это её заглушка: клиент просто называет себя.
// Когда появится настоящая, читать заголовок будет middleware, а хендлеры
// останутся прежними — в этом и смысл заголовка вместо префикса в пути.
// Личность в URL утекает в логи, Referer и историю браузера, а сменить схему
// потом можно только сломав все ссылки.
const ownerHeader = "X-Owner-ID"

// TaskService — то, что транспорту нужно от слоя сценариев.
//
// Интерфейс объявлен у потребителя, как и порты в app: транспорту не нужен
// весь *app.TaskService, а тестам хендлеров нужен двойник, умеющий вернуть
// любую ошибку сценария — включая те, которые на настоящем сервисе
// подстроить трудно.
type TaskService interface {
	CreateTask(ctx context.Context, cmd app.CreateTaskCommand) (todo.TaskID, error)
	GetTask(ctx context.Context, query app.GetTaskQuery) (app.TaskView, error)
	UpdateTask(ctx context.Context, cmd app.UpdateTaskCommand) error
	StartTask(ctx context.Context, cmd app.StartTaskCommand) error
	CompleteTask(ctx context.Context, cmd app.CompleteTaskCommand) error
	CancelTask(ctx context.Context, cmd app.CancelTaskCommand) error
}

// Handler обслуживает HTTP-запросы к задачам.
type Handler struct {
	service TaskService
}

// NewHandler собирает транспорт. Сервис обязателен: nil здесь — паника
// на первом же запросе вместо внятного отказа при сборке.
func NewHandler(service TaskService) (*Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("httpapi: build handler (task service): %w", app.ErrMissingDependency)
	}
	return &Handler{service: service}, nil
}

// Routes отдаёт маршрутизатор со всеми обработчиками.
//
// Маршрутизация — штатная в net/http: шаблоны с методом и переменной пути
// появились в 1.22, поэтому внешний роутер не нужен. Несовпадение метода
// на известном пути мультиплексор превращает в 405 сам.
//
// Переходы статуса вынесены в действия, а не в поле PATCH: статус живёт
// в конечном автомате с transitionMatrix, и «присвоить статус» — не та
// операция, которую предлагает домен.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /tasks", h.createTask)
	mux.HandleFunc("GET /tasks/{id}", h.getTask)
	mux.HandleFunc("PATCH /tasks/{id}", h.updateTask)
	mux.HandleFunc("POST /tasks/{id}/start", h.startTask)
	mux.HandleFunc("POST /tasks/{id}/complete", h.completeTask)
	mux.HandleFunc("POST /tasks/{id}/cancel", h.cancelTask)

	return mux
}

// taskResponse — задача на проводе.
//
// Плоская и строковая, как app.TaskView, из которой она и собирается. Имена
// полей в snake_case, времена — RFC 3339. Необязательные поля кодируются
// указателями и уезжают как null, а не пропадают: клиент, читающий ответ,
// должен видеть разницу между «срока нет» и «поле не пришло».
type taskResponse struct {
	ID          string     `json:"id"`
	OwnerID     string     `json:"owner_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	DueDate     *time.Time `json:"due_date"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Version     int        `json:"version"`
}

// createTaskRequest — тело POST /tasks.
//
// Владелец сюда не входит: он приезжает заголовком, и принимать его ещё и
// в теле значило бы завести второй источник правды о том, кто пишет.
type createTaskRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    string     `json:"priority"`
	DueDate     *time.Time `json:"due_date"`
}

// updateTaskRequest — тело PATCH /tasks/{id}.
//
// У каждого поля три состояния, и различать их обязан разбор: поля нет —
// не трогать, поле null — снять или очистить, поле со значением — назначить.
// Стандартный json.Unmarshal сам этого не даёт: и отсутствие, и null дают
// нулевой указатель. Отсюда json.RawMessage — сырой кусок хранит разницу,
// а разбирается уже вручную.
type updateTaskRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Priority    *string `json:"priority"`
	// DueDate разбирается отдельно: nil — поля не было, «null» — снять срок,
	// строка — назначить.
	DueDate json.RawMessage `json:"due_date"`
}

// errorResponse — тело любого отказа.
//
// Одно поле: разбирать причину машинно клиенту незачем, для этого есть код
// ответа, а текст нужен человеку в логе. Заводить код ошибки строкой стоит
// тогда, когда появится клиент, которому этого не хватит.
type errorResponse struct {
	Error string `json:"error"`
}

// statusFor переводит ошибку сценария в код ответа.
//
// Единственное место, где транспорт знает про сентинели app и todo. Порядок
// проверок важен: ErrTaskNotFound идёт раньше валидации, потому что чужая
// задача обязана выглядеть как несуществующая.
func statusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, app.ErrTaskNotFound):
		return http.StatusNotFound
	case errors.Is(err, app.ErrVersionConflict):
		return http.StatusConflict
	case errors.Is(err, todo.ErrInvalidStatusTransition),
		errors.Is(err, todo.ErrTaskAlreadyCompleted),
		errors.Is(err, todo.ErrTaskCancelled):
		// Конфликт с текущим состоянием, а не негодный запрос: тот же самый
		// запрос был бы законен, застань он задачу в другом статусе.
		return http.StatusConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout
	}

	// TODO: доменная валидация — 400, всё неопознанное — 500.
	// Ошибка разбора команды и отказ хранилища различаются не текстом:
	// первая приходит из фабрик todo, второй — из порта. Отдельный признак
	// для этого заводить не надо, сентинелей todo конечное число.
	return http.StatusNotImplemented
}

// Заглушки. Каждая отвечает 501, а не паникует: паника в хендлере уронила бы
// тестовый бинарь на первом же случае, и остальная краснота осталась бы
// невидимой. С 501 весь набор падает разом и показывает полный список.

func (h *Handler) createTask(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w)
}

func (h *Handler) getTask(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w)
}

func (h *Handler) updateTask(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w)
}

func (h *Handler) startTask(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w)
}

func (h *Handler) completeTask(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w)
}

func (h *Handler) cancelTask(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w)
}

func notImplemented(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: "not implemented"})
}
