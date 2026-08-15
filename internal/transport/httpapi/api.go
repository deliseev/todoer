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
//
// Файл разложен сверху вниз по ходу запроса: сборка и маршруты, обработчики,
// формы запросов и ответов на проводе, общий хвост ответа и перевод ошибок.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// Сборка и маршруты.

// ownerHeader — заголовок, из которого берётся владелец запроса.
//
// Аутентификации пока нет, и это её заглушка: клиент просто называет себя.
// Когда появится настоящая, меняться будет только withOwner, а обработчики
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
//
// Владелец нужен всем маршрутам без исключения, поэтому каждый обработчик
// завёрнут в withOwner.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /tasks", withOwner(h.createTask))
	mux.HandleFunc("GET /tasks/{id}", withOwner(h.getTask))
	mux.HandleFunc("PATCH /tasks/{id}", withOwner(h.updateTask))
	mux.HandleFunc("POST /tasks/{id}/start", withOwner(h.startTask))
	mux.HandleFunc("POST /tasks/{id}/complete", withOwner(h.completeTask))
	mux.HandleFunc("POST /tasks/{id}/cancel", withOwner(h.cancelTask))

	return mux
}

// ownerHandler — обработчик, которому владелец уже разобран.
//
// Владелец приезжает параметром, а не через контекст запроса: из контекста
// хендлер достаёт any, и компилятор ничего не обещает. Забытый на новом
// маршруте withOwner дал бы тогда пустого владельца, отказ приехал бы из
// фабрик todo — после чтения и с другим кодом. Здесь же обработчик с тремя
// параметрами в HandleFunc просто не подставляется: пару «маршрут —
// владелец» держит компилятор, а не память пишущего.
type ownerHandler func(w http.ResponseWriter, r *http.Request, owner string)

// withOwner достаёт владельца запроса из заголовка и отказывает без него.
//
// Пустой заголовок равен отсутствующему: пробелы — не имя. Отказ обязан
// случиться до похода в app — сценарию нечего спрашивать без владельца.
// 400, а не 401: схемы аутентификации нет, а 401 обязывает прислать
// WWW-Authenticate. Заголовок здесь — часть формы запроса.
//
// Оборачивается каждый маршрут, а не мультиплексор целиком: обёртка вокруг
// него встала бы раньше маршрутизации, и запрос к несуществующему пути без
// заголовка получал бы 400 вместо 404.
//
// Когда появится настоящая аутентификация, менять придётся только нутро:
// владельца положит в контекст её middleware, а сигнатуры обработчиков
// останутся прежними.
func withOwner(next ownerHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner := strings.TrimSpace(r.Header.Get(ownerHeader))
		if owner == "" {
			writeError(w, http.StatusBadRequest, "missing "+ownerHeader+" header")
			return
		}
		next(w, r, owner)
	}
}

// Обработчики. Каждый разбирает запрос, зовёт ровно один сценарий и отвечает;
// решений он не принимает и доменных правил не перепроверяет.

// createTask создаёт задачу: POST /tasks.
//
// Отвечает 201 с Location на созданный ресурс. Собирать тело из присланного
// нельзя: клиент увидел бы не то, что лежит в хранилище, — статус, версию
// и времена назначает домен.
func (h *Handler) createTask(w http.ResponseWriter, r *http.Request, owner string) {
	var req createTaskRequest
	if !decodeBody(w, r, &req) {
		return
	}

	id, err := h.service.CreateTask(r.Context(), app.CreateTaskCommand{
		OwnerID:     owner,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		DueDate:     req.DueDate,
	})
	if !written(err) {
		failure(w, err)
		return
	}

	// Location указывает на созданный ресурс, тело несёт тот же идентификатор:
	// заголовок — для клиента, ходящего по ссылкам, поле — для того, кто
	// разбирает JSON, и разбирать Location ради идентификатора ему не нужно.
	w.Header().Set("Location", "/tasks/"+id.String())
	writeJSON(w, http.StatusCreated, createdResponse{ID: id.String()})
}

// getTask отдаёт задачу целиком: GET /tasks/{id}.
//
// Идентификатор уезжает в запрос сырой строкой: разбирать его — работа app,
// у транспорта нет ни фабрик todo, ни права их знать. Негодный идентификатор
// поэтому стоит одного обращения к сценарию и возвращается его же ошибкой.
//
// Про маскировку чужой задачи под несуществующую здесь помнить не надо:
// сценарий уже отдал ErrTaskNotFound, и различать тут нечего.
func (h *Handler) getTask(w http.ResponseWriter, r *http.Request, owner string) {
	view, err := h.service.GetTask(r.Context(), app.GetTaskQuery{
		TaskID:  r.PathValue("id"),
		OwnerID: owner,
	})
	if err != nil {
		failure(w, err)
		return
	}

	writeJSON(w, http.StatusOK, newTaskResponse(view))
}

// updateTask меняет заданные поля задачи: PATCH /tasks/{id}.
//
// Пустоту команды транспорт не проверяет: «изменение без полей» — правило
// сценария (app.ErrEmptyUpdate), и повторять его здесь значило бы завести
// второго носителя одного правила.
func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request, owner string) {
	var req updateTaskRequest
	if !decodeBody(w, r, &req) {
		return
	}

	dueDate, err := parseDueDateUpdate(req.DueDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed due_date")
		return
	}

	taskID := r.PathValue("id")
	if err := h.service.UpdateTask(r.Context(), app.UpdateTaskCommand{
		TaskID:      taskID,
		OwnerID:     owner,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		DueDate:     dueDate,
	}); !written(err) {
		failure(w, err)
		return
	}

	h.respondTask(w, r, taskID, owner)
}

// startTask берёт задачу в работу: POST /tasks/{id}/start.
func (h *Handler) startTask(w http.ResponseWriter, r *http.Request, owner string) {
	h.changeStatus(w, r, owner, func(ctx context.Context, taskID, owner string) error {
		return h.service.StartTask(ctx, app.StartTaskCommand{TaskID: taskID, OwnerID: owner})
	})
}

// completeTask отмечает задачу выполненной: POST /tasks/{id}/complete.
func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request, owner string) {
	h.changeStatus(w, r, owner, func(ctx context.Context, taskID, owner string) error {
		return h.service.CompleteTask(ctx, app.CompleteTaskCommand{TaskID: taskID, OwnerID: owner})
	})
}

// cancelTask отменяет задачу: POST /tasks/{id}/cancel.
func (h *Handler) cancelTask(w http.ResponseWriter, r *http.Request, owner string) {
	h.changeStatus(w, r, owner, func(ctx context.Context, taskID, owner string) error {
		return h.service.CancelTask(ctx, app.CancelTaskCommand{TaskID: taskID, OwnerID: owner})
	})
}

// statusAction — вызов сценария перехода.
//
// У трёх команд разные типы с одинаковыми полями, поэтому общего у действий
// ровно столько: идентификатор, владелец и ошибка.
type statusAction func(ctx context.Context, taskID, owner string) error

// changeStatus — общий хвост трёх действий над статусом.
//
// Тела у этих запросов нет, разбирать нечего, и различаются они одним вызовом
// сценария. Расписывать вокруг него одно и то же трижды нельзя: так теряются
// то разбор отказа доставки, то перечитывание задачи в ответ.
func (h *Handler) changeStatus(w http.ResponseWriter, r *http.Request, owner string, action statusAction) {
	taskID := r.PathValue("id")
	if err := action(r.Context(), taskID, owner); !written(err) {
		failure(w, err)
		return
	}

	h.respondTask(w, r, taskID, owner)
}

// Запросы и ответы на проводе.

// maxBodyBytes — потолок размера тела запроса.
//
// Законное тело ограничено доменом: заголовок — 256 рун, описание — 4096,
// то есть в худшем случае с экранированием десятки килобайт. Отсюда 64 КиБ
// с запасом. Числа домена сюда не импортируются: это предел провода,
// а не доменное правило, и связывать их — значит менять форму HTTP при
// каждой правке ограничений задачи.
const maxBodyBytes = 64 << 10

// decodeBody разбирает тело запроса, а на негодном сам отвечает отказом.
//
// Потолок нужен потому, что транспорт разбирает раньше, чем сценарий проверит
// длину: без него гигабайтный заголовок сначала целиком оказался бы в памяти
// и только потом получил бы 400 за длину, а несколько таких запросов положили
// бы процесс. Превышение — 413, а не 400: дело не в форме запроса, а в его
// размере, и клиенту стоит знать, что укоротить надо тело, а не поля.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	// MaxBytesReader вешается на ответ, а не только на чтение: он же закрывает
	// соединение, чтобы отправитель не досылал остаток в никуда.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	decoder := json.NewDecoder(r.Body)
	// Неизвестное поле — отказ, а не молчаливый пропуск. Опечатка в имени
	// иначе отвечает успехом, ничего не изменив, и клиенту неоткуда узнать,
	// что его правку выбросили по дороге.
	decoder.DisallowUnknownFields()

	err := decoder.Decode(dst)
	if err == nil {
		err = expectEOF(decoder)
	}

	switch {
	case err == nil:
		return true
	case isTooLarge(err):
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", maxBodyBytes))
	default:
		// Сюда же попадает срок не по RFC 3339 — его разбирает сам
		// encoding/json, и до сценария такое тело не доходит.
		writeError(w, http.StatusBadRequest, "malformed request body")
	}
	return false
}

// expectEOF убеждается, что за разобранным документом в теле ничего нет.
//
// Тело — один документ, а не поток: второй молча пропал бы, и клиент,
// приславший два, считал бы принятыми оба. Ошибка чтения отдаётся как есть —
// в неё может прийти и превышение потолка, а его надо отличить от негодного
// тела этажом выше.
func expectEOF(decoder *json.Decoder) error {
	switch err := decoder.Decode(new(struct{})); {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return err
	default:
		return errors.New("httpapi: unexpected json document after the first")
	}
}

// isTooLarge отличает превышение потолка от прочих бед разбора.
func isTooLarge(err error) bool {
	_, ok := errors.AsType[*http.MaxBytesError](err)
	return ok
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

// parseDueDateUpdate разбирает три состояния срока в теле PATCH.
//
// Пустой кусок — поля не было, значит не трогать. Дальше работу делает сам
// encoding/json: «null» оставляет At нулевым (снять срок), строка по
// RFC 3339 его заполняет (назначить), всё прочее — ошибка разбора.
func parseDueDateUpdate(raw json.RawMessage) (*app.DueDateUpdate, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var at *time.Time
	if err := json.Unmarshal(raw, &at); err != nil {
		return nil, err
	}
	return &app.DueDateUpdate{At: at}, nil
}

// createdResponse — тело ответа на создание.
//
// Одно поле, и это не заготовка под задачу целиком: остальное клиент только
// что прислал сам, а статус, версию и времена назначает домен — собери мы
// их здесь из присланного, клиент увидел бы не то, что лежит в хранилище.
// Кому нужна задача целиком, тот идёт по Location.
type createdResponse struct {
	ID string `json:"id"`
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

// newTaskResponse перекладывает представление сценария на провод.
//
// Указатели переносятся как есть: TaskView отдана транспорту в личное
// пользование, и дальше ответа они не живут.
func newTaskResponse(view app.TaskView) taskResponse {
	return taskResponse{
		ID:          view.ID,
		OwnerID:     view.OwnerID,
		Title:       view.Title,
		Description: view.Description,
		Status:      view.Status,
		Priority:    view.Priority,
		DueDate:     view.DueDate,
		CreatedAt:   view.CreatedAt,
		UpdatedAt:   view.UpdatedAt,
		CompletedAt: view.CompletedAt,
		Version:     view.Version,
	}
}

// errorResponse — тело любого отказа.
//
// Одно поле: разбирать причину машинно клиенту незачем, для этого есть код
// ответа, а текст нужен человеку в логе. Заводить код ошибки строкой стоит
// тогда, когда появится клиент, которому этого не хватит.
type errorResponse struct {
	Error string `json:"error"`
}

// Общий хвост записи и перевод ошибок в коды.

// written сообщает, состоялась ли запись, и уводит отказ доставки в лог.
//
// Отказ доставки — всё равно успех: задача записана и видна всем, кто её
// прочтёт. 5xx заставил бы клиента повторить запись, которая уже состоялась,
// а POST /tasks вдобавок неидемпотентен — на повторе появилась бы вторая
// задача. Да и повтор ничего не доставил бы: для этого нужен outbox.
func written(err error) bool {
	if delivery, ok := errors.AsType[*app.EventDeliveryError](err); ok {
		slog.Error("deliver task events", "task", delivery.TaskID, "error", delivery.Err)
		return true
	}
	return err == nil
}

// respondTask отвечает задачей, перечитав её после успешной записи.
//
// Мутирующие сценарии возвращают только ошибку, поэтому ответ стоит лишнего
// GetTask. Отказ этого чтения — 500: запись состоялась, задача есть, и 404
// был бы враньём. Ошибку сценария сюда пускать нельзя по той же причине —
// отсюда свой ответ вместо failure.
func (h *Handler) respondTask(w http.ResponseWriter, r *http.Request, taskID, owner string) {
	view, err := h.service.GetTask(r.Context(), app.GetTaskQuery{
		TaskID:  taskID,
		OwnerID: owner,
	})
	if err != nil {
		slog.Error("read back written task", "task", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, statusText(http.StatusInternalServerError))
		return
	}

	writeJSON(w, http.StatusOK, newTaskResponse(view))
}

// failure отвечает отказом по ошибке сценария.
//
// Текст ответа — свой, выведенный из кода: чужой наружу не уезжает. Ошибка
// хранилища может нести имя таблицы, путь или адрес соседнего сервиса,
// а клиенту довольно кода.
func failure(w http.ResponseWriter, err error) {
	status := statusFor(err)
	writeError(w, status, statusText(status))
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
	case errors.Is(err, todo.ErrEmptyTitle),
		errors.Is(err, todo.ErrTitleTooLong),
		errors.Is(err, todo.ErrDescriptionTooLong),
		errors.Is(err, todo.ErrInvalidTaskID),
		errors.Is(err, todo.ErrInvalidOwnerID),
		errors.Is(err, todo.ErrDueDateInPast),
		errors.Is(err, todo.ErrUnknownPriority),
		errors.Is(err, todo.ErrUnknownStatus),
		errors.Is(err, app.ErrEmptyUpdate):
		// Негодный ввод: команда не разобралась в значимые объекты домена
		// либо не несёт ни одного поля. Сентинели перечислены поимённо, а не
		// сведены к правилу «всё из todo — 400»: конфликт состояния приходит
		// оттуда же, и такое правило отвечало бы на него 400 вместо 409.
		return http.StatusBadRequest
	}

	// Всё неопознанное — 500. Ошибка разбора команды и отказ хранилища
	// различаются не текстом: первая приходит из фабрик todo и перечислена
	// выше, второй — из порта, и отдельный признак для него заводить не надо.
	return http.StatusInternalServerError
}

// statusText — текст отказа по коду ответа: «not found», «conflict».
// Регистр нижний, как у всех ошибок в проекте.
func statusText(status int) string {
	return strings.ToLower(http.StatusText(status))
}

// writeError отвечает телом отказа.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// writeJSON отправляет тело ответа.
//
// Заголовки выставляются до WriteHeader: после него они уже уехали. Отказ
// самой записи игнорируется намеренно — ответ начат, кода для второй попытки
// нет, и сказать об этом можно только в лог.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response body", "status", status, "error", err)
	}
}
