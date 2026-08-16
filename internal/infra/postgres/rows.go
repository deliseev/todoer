package postgres

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// taskRow — задача в том виде, в каком она лежит в таблице: плоская, из строк
// и моментов времени, без единого доменного типа.
//
// Отдельным типом, а не набором переменных на месте: это отображение —
// граница между базой и доменом, и держать её в одном месте нужно затем же,
// зачем домен держит границу со снимком.
//
// Связь с колонками именная, а не позиционная. Позиционная связь здесь была,
// и она хрупка не теоретически: title и description — оба text и стоят рядом,
// так что перестановка двух аргументов компилировалась бы, выполнялась бы и
// писала описание в заголовок. Теперь и чтение (RowToStructByName по тегам
// db), и запись (StrictNamedArgs) сверяются по именам, а расхождение
// становится громкой ошибкой вместо тихой подстановки.
//
// Поля экспортированы, потому что разбор по именам работает только с
// открытыми полями; сам тип при этом пакетный и наружу не уезжает.
type taskRow struct {
	ID          string     `db:"id"`
	OwnerID     string     `db:"owner_id"`
	Title       string     `db:"title"`
	Description string     `db:"description"`
	Status      string     `db:"status"`
	Priority    string     `db:"priority"`
	DueDate     *time.Time `db:"due_date"`
	CompletedAt *time.Time `db:"completed_at"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	Version     int        `db:"version"`
}

// newTaskRow раскладывает снимок задачи по колонкам.
func newTaskRow(snapshot todo.TaskSnapshot) taskRow {
	row := taskRow{
		ID:          snapshot.ID.String(),
		OwnerID:     snapshot.OwnerID.String(),
		Title:       snapshot.Title.String(),
		Description: snapshot.Description.String(),
		Status:      snapshot.Status.String(),
		Priority:    snapshot.Priority.String(),
		CreatedAt:   snapshot.CreatedAt,
		UpdatedAt:   snapshot.UpdatedAt,
		Version:     snapshot.Version,
	}

	// Необязательное копируется, а не переносится указателем: снимок отдал
	// свои копии, но дальше они уедут в базу и обратно, и делить их с кем бы
	// то ни было незачем.
	if snapshot.DueDate != nil {
		at := snapshot.DueDate.Time()
		row.DueDate = &at
	}
	if snapshot.CompletedAt != nil {
		at := *snapshot.CompletedAt
		row.CompletedAt = &at
	}

	return row
}

// args отдаёт значения строки под именами параметров запроса.
//
// Карта, а не срез: у карты нет порядка, поэтому перепутать в ней аргументы
// местами физически нечем.
//
// Строгий вариант, а не обычный NamedArgs, и это не придирка: обычный молча
// подставляет NULL вместо параметра, которого не нашёл в карте. Для колонок
// с NOT NULL это ловит база, а для необязательных — никто: опечатка в ключе
// due_date просто теряла бы срок. Измерено: тест увидел «срок потерян», база
// не возразила ни словом. StrictNamedArgs считает ошибкой и параметр, который
// запрос ждёт и не получил, и лишний, которого запрос не ждёт.
func (r taskRow) args() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"id":           r.ID,
		"owner_id":     r.OwnerID,
		"title":        r.Title,
		"description":  r.Description,
		"status":       r.Status,
		"priority":     r.Priority,
		"due_date":     r.DueDate,
		"completed_at": r.CompletedAt,
		"created_at":   r.CreatedAt,
		"updated_at":   r.UpdatedAt,
		"version":      r.Version,
	}
}

// snapshot собирает доменный снимок из строки таблицы.
//
// Все значимые объекты создаются своими фабриками: строка в базе могла быть
// написана чем угодно — прежней версией кода, миграцией, рукой в psql, — и
// доверять ей на слово хранилище не вправе. Исключение одно, и оно доменное:
// срок восстанавливается через ReconstituteDueDate, потому что его инвариант
// задан относительно «сейчас» и кругового пути через базу не переживает.
func (r taskRow) snapshot() (todo.TaskSnapshot, error) {
	id, err := todo.ParseTaskID(r.ID)
	if err != nil {
		return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task id: %w", err)
	}
	ownerID, err := todo.ParseOwnerID(r.OwnerID)
	if err != nil {
		return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task %s owner: %w", r.ID, err)
	}
	title, err := todo.NewTitle(r.Title)
	if err != nil {
		return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task %s title: %w", r.ID, err)
	}
	description, err := todo.NewDescription(r.Description)
	if err != nil {
		return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task %s description: %w", r.ID, err)
	}
	status, err := todo.ParseStatus(r.Status)
	if err != nil {
		return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task %s status: %w", r.ID, err)
	}
	priority, err := todo.ParsePriority(r.Priority)
	if err != nil {
		return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task %s priority: %w", r.ID, err)
	}

	snapshot := todo.TaskSnapshot{
		ID:          id,
		OwnerID:     ownerID,
		Title:       title,
		Description: description,
		Status:      status,
		Priority:    priority,
		// Моменты нормализуются в UTC: pgx отдаёт их в зоне сессии, и без
		// этого одна и та же задача выглядела бы по-разному в зависимости от
		// настроек базы.
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
		Version:   r.Version,
	}

	if r.DueDate != nil {
		dueDate, err := todo.ReconstituteDueDate(*r.DueDate)
		if err != nil {
			return todo.TaskSnapshot{}, fmt.Errorf("postgres: read task %s due date: %w", r.ID, err)
		}
		snapshot.DueDate = &dueDate
	}
	if r.CompletedAt != nil {
		completedAt := r.CompletedAt.UTC()
		snapshot.CompletedAt = &completedAt
	}

	return snapshot, nil
}
