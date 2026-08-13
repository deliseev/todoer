package todo

import "time"

// Task — корень агрегата и единственная транзакционная граница домена задач.
//
// Все поля неэкспортируемые: снаружи задача меняется только через методы,
// которые сами следят за инвариантами, версией и порождением событий.
// Связи с другими агрегатами хранятся по идентичности (OwnerID), а не ссылкой.
type Task struct {
	id          TaskID
	ownerID     OwnerID
	title       Title
	description Description
	status      Status
	priority    Priority
	dueDate     *DueDate // nil — срок не задан

	createdAt   time.Time
	updatedAt   time.Time
	completedAt *time.Time // nil, пока задача не выполнена

	// version растёт на каждой успешной мутации и служит основой
	// оптимистичной блокировки в хранилище.
	version int

	// events копятся до вызова PullEvents.
	events []DomainEvent
}

// TaskSnapshot — плоское представление задачи для пересечения границы
// агрегата: хранилище сохраняет снимок и восстанавливает задачу из него.
type TaskSnapshot struct {
	ID          TaskID
	OwnerID     OwnerID
	Title       Title
	Description Description
	Status      Status
	Priority    Priority
	DueDate     *DueDate
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
	Version     int
}

// NewTask создаёт новую задачу и порождает событие TaskCreated.
// Аргумент dueDate необязателен: nil означает задачу без срока.
func NewTask(
	id TaskID,
	ownerID OwnerID,
	title Title,
	description Description,
	priority Priority,
	dueDate *DueDate,
	now time.Time,
) (*Task, error) {
	id, err := ParseTaskID(id.value)
	if err != nil {
		return nil, err
	}
	ownerID, err = ParseOwnerID(ownerID.value)
	if err != nil {
		return nil, err
	}
	title, err = NewTitle(title.value)
	if err != nil {
		return nil, err
	}
	priority, err = ParsePriority(priority.String())
	if err != nil {
		return nil, err
	}
	return &Task{
		id:          id,
		ownerID:     ownerID,
		title:       title,
		description: description,
		priority:    priority,
		dueDate:     dueDate,
		createdAt:   now,
		updatedAt:   now,
		version:     1,
		events: []DomainEvent{TaskCreated{
			eventMeta: eventMeta{
				taskID: id,
				at:     now,
			},
			OwnerID:     ownerID,
			Title:       title,
			Description: description,
			Priority:    priority,
			DueDate:     dueDate,
		}},
	}, nil
}

// ReconstituteTask восстанавливает задачу из снимка, не порождая событий:
// поднятие из хранилища — не факт доменной жизни, а технический шаг.
func ReconstituteTask(s TaskSnapshot) (*Task, error) {
	task, err := NewTask(
		s.ID,
		s.OwnerID,
		s.Title,
		s.Description,
		s.Priority,
		s.DueDate,
		s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if !s.Status.IsValid() {
		return nil, ErrUnknownStatus
	}
	task.status = s.Status
	task.version = s.Version
	task.updatedAt = s.UpdatedAt
	task.createdAt = s.CreatedAt
	task.events = make([]DomainEvent, 0)
	return task, nil
}

// Snapshot возвращает плоское представление текущего состояния задачи.
func (t *Task) Snapshot() TaskSnapshot {
	return TaskSnapshot{
		ID:          t.id,
		OwnerID:     t.ownerID,
		Title:       t.title,
		Description: t.description,
		Status:      t.status,
		Priority:    t.priority,
		DueDate:     t.dueDate,
		CreatedAt:   t.createdAt,
		UpdatedAt:   t.updatedAt,
		CompletedAt: t.completedAt,
		Version:     t.version,
	}
}

// ID возвращает идентификатор задачи.
func (t *Task) ID() TaskID { return t.id }

// OwnerID возвращает идентификатор владельца задачи.
func (t *Task) OwnerID() OwnerID { return t.ownerID }

// Title возвращает заголовок задачи.
func (t *Task) Title() Title { return t.title }

// Description возвращает описание задачи.
func (t *Task) Description() Description { return t.description }

// Status возвращает текущий статус задачи.
func (t *Task) Status() Status { return t.status }

// Priority возвращает приоритет задачи.
func (t *Task) Priority() Priority { return t.priority }

// DueDate возвращает срок выполнения. Второе значение равно false,
// если срок не задан.
func (t *Task) DueDate() (DueDate, bool) {
	if t.dueDate == nil {
		return DueDate{}, false
	}
	return *t.dueDate, true
}

// CreatedAt возвращает момент создания задачи.
func (t *Task) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt возвращает момент последнего успешного изменения задачи.
func (t *Task) UpdatedAt() time.Time { return t.updatedAt }

// CompletedAt возвращает момент выполнения задачи. Второе значение равно
// false, если задача ещё не выполнена.
func (t *Task) CompletedAt() (time.Time, bool) {
	if t.completedAt == nil {
		return time.Time{}, false
	}
	return *t.completedAt, true
}

// Version возвращает версию агрегата.
func (t *Task) Version() int { return t.version }

// Rename меняет заголовок задачи.
func (t *Task) Rename(title Title, now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	title, err := NewTitle(title.value)
	if err != nil {
		return err
	}
	t.title = title
	t.updatedAt = now
	t.version += 1

	e := TaskRenamed{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
		NewTitle: title,
	}
	t.events = append(t.events, e)

	return nil
}

// Describe меняет описание задачи.
func (t *Task) Describe(description Description, now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	t.description = description
	t.updatedAt = now
	t.version += 1

	e := TaskDescribed{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
		NewDescription: description,
	}
	t.events = append(t.events, e)

	return nil
}

// ChangePriority меняет приоритет задачи.
func (t *Task) ChangePriority(priority Priority, now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	priority, err := ParsePriority(priority.String())
	if err != nil {
		return err
	}
	t.priority = priority
	t.version += 1

	e := TaskPriorityChanged{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
		NewPriority: priority,
	}
	t.events = append(t.events, e)

	return nil
}

// Reschedule назначает, переносит или снимает срок выполнения.
// Nil снимает срок. Срок, не наступающий в будущем относительно now,
// отвергается даже если он был корректен в момент своего создания.
func (t *Task) Reschedule(dueDate *DueDate, now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	if dueDate != nil && dueDate.IsBefore(now) {
		return ErrDueDateInPast
	}
	t.dueDate = dueDate
	t.version += 1

	e := TaskRescheduled{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
		NewDueDate: dueDate,
	}
	t.events = append(t.events, e)

	return nil
}

// Start переводит задачу в работу.
func (t *Task) Start(now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	if t.status == StatusCancelled {
		return ErrTaskCancelled
	}
	allow := t.status.CanTransitionTo(StatusInProgress)
	if !allow {
		return ErrInvalidStatusTransition
	}
	t.status = StatusInProgress
	t.updatedAt = now
	t.version += 1

	e := TaskStarted{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
	}
	t.events = append(t.events, e)

	return nil
}

// Complete отмечает задачу выполненной.
func (t *Task) Complete(now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	if t.status == StatusCancelled {
		return ErrTaskCancelled
	}
	t.status = StatusCompleted
	t.completedAt = &now
	t.version += 1

	e := TaskCompleted{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
	}
	t.events = append(t.events, e)

	return nil
}

// Cancel отменяет задачу.
func (t *Task) Cancel(now time.Time) error {
	if t.status == StatusCompleted {
		return ErrTaskAlreadyCompleted
	}
	if t.status == StatusCancelled {
		return ErrTaskCancelled
	}
	t.status = StatusCancelled
	t.completedAt = nil
	t.version += 1

	e := TaskCancelled{
		eventMeta: eventMeta{
			taskID: t.id,
			at:     now,
		},
	}
	t.events = append(t.events, e)

	return nil
}

// IsOverdue сообщает, что срок выполнения истёк к моменту now.
// Задача в терминальном статусе просроченной не считается.
func (t *Task) IsOverdue(now time.Time) bool {
	if t.status.IsTerminal() {
		return false
	}
	if t.dueDate == nil {
		return false
	}
	return t.dueDate.IsBefore(now)
}

// PullEvents возвращает накопленные события в порядке их возникновения
// и очищает внутренний буфер: повторный вызов вернёт пустой срез.
func (t *Task) PullEvents() []DomainEvent {
	events := t.events
	t.events = make([]DomainEvent, 0)
	return events
}
