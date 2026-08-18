package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/infra/outbox"
)

// outboxRow — событие в том виде, в каком оно ложится в очередь: имя, задача,
// момент и поля, свёрнутые в JSON.
//
// Тем же типом и по той же причине, что taskRow: отображение между доменом и
// колонками — граница, и держать её надо в одном месте.
type outboxRow struct {
	AggregateID string
	Name        string
	Payload     []byte
	OccurredAt  time.Time
}

// args отдаёт значения строки под именами параметров запроса — строгими,
// как и у задачи: молча подставленный NULL здесь означал бы событие без полей.
func (r outboxRow) args() pgx.StrictNamedArgs {
	return pgx.StrictNamedArgs{
		"aggregate_id": r.AggregateID,
		"name":         r.Name,
		"payload":      r.Payload,
		"occurred_at":  r.OccurredAt,
	}
}

// messageRow — строка очереди в том виде, в каком её читает доставщик.
//
// Отдельно от outboxRow, потому что это другая сторона границы: та кладёт
// событие в очередь, эта достаёт сообщение из неё, и колонки у них разные —
// на запись не нужен номер, на чтение не нужны отметки доставки.
type messageRow struct {
	ID          int64     `db:"id"`
	AggregateID string    `db:"aggregate_id"`
	Name        string    `db:"name"`
	Payload     []byte    `db:"payload"`
	OccurredAt  time.Time `db:"occurred_at"`
}

// message собирает сообщение доставщика из строки таблицы.
//
// Доменных фабрик здесь нет, и это не забывчивость: доставщику нужны имя,
// адресат и нагрузка, а не значимые объекты. Собирать из строки агрегат,
// чтобы тут же разложить его обратно в JSON, значило бы городить круг ради
// проверки, которую эта строка уже прошла на записи.
func (r messageRow) message() outbox.Message {
	return outbox.Message{
		ID:          r.ID,
		AggregateID: r.AggregateID,
		Name:        r.Name,
		Payload:     r.Payload,
		// Момент нормализуется в UTC: pgx отдаёт его в зоне сессии.
		OccurredAt: r.OccurredAt.UTC(),
	}
}

// newOutboxRow раскладывает доменное событие по колонкам очереди.
//
// Разбор именно такой — руками, по типам, — потому что encoding/json домену
// не поможет: значимые объекты хранят своё в закрытых полях, и обычная
// сериализация выдала бы `{}` вместо заголовка. Поля берутся теми же
// методами, которыми их читает кто угодно снаружи.
//
// Незнакомое событие — ошибка, а не пустая нагрузка: новое событие, о котором
// забыли здесь, иначе уехало бы в очередь пустышкой и обнаружилось бы у
// потребителя, когда чинить уже поздно.
func newOutboxRow(event todo.DomainEvent) (outboxRow, error) {
	payload, err := eventPayload(event)
	if err != nil {
		return outboxRow{}, err
	}

	return outboxRow{
		AggregateID: event.AggregateID().String(),
		Name:        event.EventName(),
		Payload:     payload,
		OccurredAt:  event.OccurredAt(),
	}, nil
}

// eventPayload сворачивает поля события в JSON.
func eventPayload(event todo.DomainEvent) ([]byte, error) {
	var payload any

	switch e := event.(type) {
	case todo.TaskCreated:
		payload = taskCreatedPayload{
			OwnerID:     e.OwnerID.String(),
			Title:       e.Title.String(),
			Description: e.Description.String(),
			Priority:    e.Priority.String(),
			DueDate:     dueDateAt(e.DueDate),
		}
	case todo.TaskRenamed:
		payload = taskRenamedPayload{Title: e.NewTitle.String()}
	case todo.TaskDescribed:
		payload = taskDescribedPayload{Description: e.NewDescription.String()}
	case todo.TaskPriorityChanged:
		payload = taskPriorityChangedPayload{Priority: e.NewPriority.String()}
	case todo.TaskRescheduled:
		payload = taskRescheduledPayload{DueDate: dueDateAt(e.NewDueDate)}
	case todo.TaskStarted, todo.TaskCompleted, todo.TaskCancelled:
		// Переходы статуса не несут полей: всё, что произошло, сказано именем
		// события и моментом. Пустой объект, а не NULL, — колонка обязательна,
		// и «полей нет» отличается от «поля потеряли».
		payload = struct{}{}
	default:
		return nil, fmt.Errorf("postgres: encode event %s of task %s (unknown event type %T)",
			event.EventName(), event.AggregateID(), event)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("postgres: encode event %s of task %s: %w",
			event.EventName(), event.AggregateID(), err)
	}

	return encoded, nil
}

// Поля событий на проводе. Отдельными типами, а не картами: имена ключей —
// такая же часть контракта, как имена самих событий, и опечатку в карте
// не поймает никто.
type (
	taskCreatedPayload struct {
		OwnerID     string     `json:"owner_id"`
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Priority    string     `json:"priority"`
		DueDate     *time.Time `json:"due_date"`
	}

	taskRenamedPayload struct {
		Title string `json:"title"`
	}

	taskDescribedPayload struct {
		Description string `json:"description"`
	}

	taskPriorityChangedPayload struct {
		Priority string `json:"priority"`
	}

	// taskRescheduledPayload: null в due_date означает, что срок сняли, —
	// поле остаётся на месте, чтобы это было видно.
	taskRescheduledPayload struct {
		DueDate *time.Time `json:"due_date"`
	}
)

// dueDateAt превращает необязательный срок в необязательный момент.
func dueDateAt(dueDate *todo.DueDate) *time.Time {
	if dueDate == nil {
		return nil
	}

	at := dueDate.Time()
	return &at
}
