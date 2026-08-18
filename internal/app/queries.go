package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// Запросы описывают намерение прочитать задачу, а команды — изменить её.
// Разделение не косметическое: чтение не порождает событий, не двигает версию
// и не нуждается в оптимистичной блокировке.

// GetTaskQuery — чтение одной задачи.
type GetTaskQuery struct {
	TaskID  string
	OwnerID string
}

// TaskView — представление задачи для чтения.
//
// Плоское и строковое: агрегат наружу не отдаётся, иначе вызывающий получил
// бы его мутаторы вместе с данными, а транспорт начал бы зависеть от домена.
// Времена остаются time.Time — их формат выбирает тот, кто сериализует.
type TaskView struct {
	ID          string
	OwnerID     string
	Title       string
	Description string
	Status      string
	Priority    string
	DueDate     *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
	Version     int
}

// ListTasksQuery — чтение списка задач владельца.
//
// Поля сырые, как и у команд: разбирать строки в значимые объекты — работа
// сценария, и это то, что позволяет транспорту ничего не знать о домене.
// Лимит тоже строка: он приезжает из строки запроса, и разбирать его на
// стороне транспорта значило бы завести там собственный отказ.
type ListTasksQuery struct {
	OwnerID string
	// Status и Priority — отбор по значению. Пустая строка означает «любой»,
	// а не нулевое значение: у статуса и приоритета нулевые значения
	// осмысленны, и спутать эти два случая — отдать не тот список.
	Status   string
	Priority string
	// Due — именованное окно по сроку: "today" или "overdue". Границы считает
	// сценарий по часам, потому что «сегодня» — это про «сейчас», а не про
	// содержимое таблицы.
	Due string
	// Sort — поле сортировки, минус впереди означает убывание.
	// Пустая строка — сортировка по умолчанию.
	Sort string
	// Limit — сколько задач вернуть. Пустая строка означает DefaultListLimit.
	Limit string
	// After — курсор последней задачи прошлой страницы. Пустая строка
	// означает первую страницу.
	After string
}

// TaskPage — страница списка.
//
// NextCursor пуст, когда следующей страницы нет: клиент останавливается по
// его отсутствию, а не по пустой странице, за которой пришлось бы сходить.
type TaskPage struct {
	Tasks      []TaskView
	NextCursor string
}

// Пределы страницы.
const (
	// DefaultListLimit — размер страницы, когда клиент его не назвал.
	DefaultListLimit = 20
	// MaxListLimit — потолок. Запрос сверх него отвергается, а не обрезается
	// молча: клиент, попросивший тысячу и получивший сотню, решил бы, что
	// задачи кончились.
	MaxListLimit = 100
)

// TaskSort — поле, по которому упорядочен список.
type TaskSort uint8

// Допустимые порядки.
const (
	SortByDueDate TaskSort = iota
	SortByPriority
	SortByCreatedAt
)

// sortNames — единственный источник правды об именах сортировок: и String,
// и разбор читают эту таблицу.
var sortNames = [...]string{
	SortByDueDate:   "due",
	SortByPriority:  "priority",
	SortByCreatedAt: "created",
}

// String возвращает имя сортировки; для значения вне перечисления — "unknown".
func (s TaskSort) String() string {
	if int(s) >= len(sortNames) {
		return "unknown"
	}
	return sortNames[s]
}

// TaskFilter — разобранный запрос списка в том виде, в каком его исполняет
// хранилище: значимые объекты домена и абсолютные моменты времени.
//
// Хранилище не знает ни про «сегодня», ни про размер страницы по умолчанию,
// ни про устройство курсора: всё это посчитано до него.
type TaskFilter struct {
	OwnerID todo.OwnerID
	// Status и Priority: nil означает «не отбирать».
	Status   *todo.Status
	Priority *todo.Priority
	// DueFrom и DueTo — полуинтервал [DueFrom, DueTo) по сроку; nil с любой
	// стороны означает отсутствие границы.
	DueFrom *time.Time
	DueTo   *time.Time

	Sort       TaskSort
	Descending bool

	// After — место, с которого продолжается выдача. Nil — первая страница.
	After *TaskCursor
	// Limit — сколько строк вернуть, не больше.
	Limit int
}

// TaskCursor — место в порядке выдачи: ключи сортировки последней отданной
// задачи и её идентификатор.
//
// Идентификатор нужен всегда: любые два ключа могут совпасть, и без него
// у страницы не было бы однозначной точки продолжения. Ключи несутся все
// сразу — они всё равно взяты из одной строки, — а какой из них смотреть,
// решает Sort.
type TaskCursor struct {
	Sort         TaskSort
	DueDate      *time.Time
	PriorityRank int
	CreatedAt    time.Time
	ID           todo.TaskID
}

// cursorPayload — курсор на проводе.
//
// Отдельный тип, потому что наружу уезжает не структура домена, а строка:
// клиент курсор не разбирает и разбирать не должен, для него это метка
// «продолжить отсюда».
type cursorPayload struct {
	Sort      string     `json:"s"`
	DueDate   *time.Time `json:"d,omitempty"`
	Rank      int        `json:"r"`
	CreatedAt time.Time  `json:"c"`
	ID        string     `json:"i"`
}

// encodeCursor превращает курсор в непрозрачную строку.
func encodeCursor(cursor TaskCursor) (string, error) {
	payload, err := json.Marshal(cursorPayload{
		Sort:      cursor.Sort.String(),
		DueDate:   cursor.DueDate,
		Rank:      cursor.PriorityRank,
		CreatedAt: cursor.CreatedAt,
		ID:        cursor.ID.String(),
	})
	if err != nil {
		return "", fmt.Errorf("app: encode list cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodeCursor разбирает курсор и проверяет, что он от той же сортировки.
//
// Курсор — место в порядке, а не в наборе: продолжи мы по нему другую
// сортировку, клиент молча получил бы выборку, не значащую ничего —
// ни продолжение, ни начало.
func decodeCursor(value string, sort TaskSort) (TaskCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return TaskCursor{}, fmt.Errorf("app: decode list cursor (%v): %w", err, ErrInvalidListQuery)
	}

	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return TaskCursor{}, fmt.Errorf("app: parse list cursor (%v): %w", err, ErrInvalidListQuery)
	}

	if payload.Sort != sort.String() {
		return TaskCursor{}, fmt.Errorf("app: read list cursor (sorted by %s, requested %s): %w",
			payload.Sort, sort, ErrInvalidListQuery)
	}

	id, err := todo.ParseTaskID(payload.ID)
	if err != nil {
		return TaskCursor{}, fmt.Errorf("app: read list cursor task id (%v): %w", err, ErrInvalidListQuery)
	}

	return TaskCursor{
		Sort:         sort,
		DueDate:      payload.DueDate,
		PriorityRank: payload.Rank,
		CreatedAt:    payload.CreatedAt,
		ID:           id,
	}, nil
}
