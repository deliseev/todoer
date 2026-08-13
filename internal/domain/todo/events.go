package todo

import "time"

// Имена доменных событий. Это часть публичного контракта: по ним события
// маршрутизируются и сериализуются, поэтому менять их так же больно,
// как менять схему таблицы.
const (
	EventTaskCreated         = "todo.task.created"
	EventTaskRenamed         = "todo.task.renamed"
	EventTaskDescribed       = "todo.task.described"
	EventTaskPriorityChanged = "todo.task.priority_changed"
	EventTaskRescheduled     = "todo.task.rescheduled"
	EventTaskStarted         = "todo.task.started"
	EventTaskCompleted       = "todo.task.completed"
	EventTaskCancelled       = "todo.task.cancelled"
)

// DomainEvent — факт, произошедший в домене. События неизменяемы и описывают
// прошлое, поэтому их имена всегда в прошедшем времени.
type DomainEvent interface {
	// EventName возвращает имя события из списка констант выше.
	EventName() string
	// AggregateID возвращает идентификатор задачи, с которой всё случилось.
	AggregateID() TaskID
	// OccurredAt возвращает момент, когда факт произошёл.
	OccurredAt() time.Time
}

// Проверка на этапе компиляции, что каждое событие удовлетворяет DomainEvent.
var (
	_ DomainEvent = TaskCreated{}
	_ DomainEvent = TaskRenamed{}
	_ DomainEvent = TaskDescribed{}
	_ DomainEvent = TaskPriorityChanged{}
	_ DomainEvent = TaskRescheduled{}
	_ DomainEvent = TaskStarted{}
	_ DomainEvent = TaskCompleted{}
	_ DomainEvent = TaskCancelled{}
)

// eventMeta — общая часть всех доменных событий. Встраивается в конкретные
// события и даёт им две трети реализации DomainEvent.
type eventMeta struct {
	taskID TaskID
	at     time.Time
}

// AggregateID возвращает идентификатор задачи.
func (m eventMeta) AggregateID() TaskID {
	return m.taskID
}

// OccurredAt возвращает момент возникновения события.
func (m eventMeta) OccurredAt() time.Time {
	return m.at
}

// TaskCreated — задача создана.
type TaskCreated struct {
	eventMeta

	OwnerID     OwnerID
	Title       Title
	Description Description
	Priority    Priority
	DueDate     *DueDate
}

// EventName возвращает имя события.
func (e TaskCreated) EventName() string { return EventTaskCreated }

// TaskRenamed — заголовок задачи изменён.
type TaskRenamed struct {
	eventMeta

	NewTitle Title
}

// EventName возвращает имя события.
func (e TaskRenamed) EventName() string { return EventTaskRenamed }

// TaskDescribed — описание задачи изменено.
type TaskDescribed struct {
	eventMeta

	NewDescription Description
}

// EventName возвращает имя события.
func (e TaskDescribed) EventName() string { return EventTaskDescribed }

// TaskPriorityChanged — приоритет задачи изменён.
type TaskPriorityChanged struct {
	eventMeta

	NewPriority Priority
}

// EventName возвращает имя события.
func (e TaskPriorityChanged) EventName() string { return EventTaskPriorityChanged }

// TaskRescheduled — срок выполнения назначен, изменён или снят.
// Nil в NewDueDate означает, что срок сняли.
type TaskRescheduled struct {
	eventMeta

	NewDueDate *DueDate
}

// EventName возвращает имя события.
func (e TaskRescheduled) EventName() string { return EventTaskRescheduled }

// TaskStarted — задача взята в работу.
type TaskStarted struct {
	eventMeta
}

// EventName возвращает имя события.
func (e TaskStarted) EventName() string { return EventTaskStarted }

// TaskCompleted — задача выполнена.
type TaskCompleted struct {
	eventMeta
}

// EventName возвращает имя события.
func (e TaskCompleted) EventName() string { return EventTaskCompleted }

// TaskCancelled — задача отменена.
type TaskCancelled struct {
	eventMeta
}

// EventName возвращает имя события.
func (e TaskCancelled) EventName() string { return EventTaskCancelled }
