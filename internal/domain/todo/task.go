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
	// Значимые объекты валидны по построению — перепроверять их содержимое
	// незачем. Убедиться нужно ровно в одном: что вместо объекта не передали
	// нулевое значение, мимо фабрики.
	if id.IsZero() {
		return nil, ErrInvalidTaskID
	}
	if ownerID.IsZero() {
		return nil, ErrInvalidOwnerID
	}
	if title.IsZero() {
		return nil, ErrEmptyTitle
	}
	if !priority.IsValid() {
		return nil, ErrUnknownPriority
	}
	// Срок мог протухнуть между своим созданием и созданием задачи —
	// то же правило, что и в Reschedule.
	if dueDate != nil && !dueDate.Time().After(now) {
		return nil, ErrDueDateInPast
	}

	task := &Task{
		id:          id,
		ownerID:     ownerID,
		title:       title,
		description: description,
		status:      StatusPending,
		priority:    priority,
		dueDate:     dueDate,
		createdAt:   now,
		updatedAt:   now,
		version:     1,
	}
	task.events = []DomainEvent{TaskCreated{
		eventMeta:   task.meta(now),
		OwnerID:     ownerID,
		Title:       title,
		Description: description,
		Priority:    priority,
		DueDate:     dueDate,
	}}

	return task, nil
}

// ReconstituteTask восстанавливает задачу из снимка, не порождая событий:
// поднятие из хранилища — не факт доменной жизни, а технический шаг.
//
// Инварианты создания здесь намеренно не применяются. Задача с давно
// просроченным сроком законно лежит в хранилище, и отказать ей в подъёме
// значило бы запереть её там навсегда. Проверяется только то, что снимок
// вообще описывает задачу.
func ReconstituteTask(s TaskSnapshot) (*Task, error) {
	if s.ID.IsZero() {
		return nil, ErrInvalidTaskID
	}
	if s.OwnerID.IsZero() {
		return nil, ErrInvalidOwnerID
	}
	if s.Title.IsZero() {
		return nil, ErrEmptyTitle
	}
	if !s.Status.IsValid() {
		return nil, ErrUnknownStatus
	}
	if !s.Priority.IsValid() {
		return nil, ErrUnknownPriority
	}

	return &Task{
		id:          s.ID,
		ownerID:     s.OwnerID,
		title:       s.Title,
		description: s.Description,
		status:      s.Status,
		priority:    s.Priority,
		dueDate:     s.DueDate,
		createdAt:   s.CreatedAt,
		updatedAt:   s.UpdatedAt,
		completedAt: s.CompletedAt,
		version:     s.Version,
	}, nil
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

// ensureMutable проверяет, что задача ещё принимает изменения.
//
// Единственный источник правды о том, кончилась ли жизнь задачи, —
// Status.IsTerminal: пока агрегат перечислял терминальные статусы сам,
// он успел разойтись со своим же конечным автоматом.
func (t *Task) ensureMutable() error {
	if !t.status.IsTerminal() {
		return nil
	}

	switch t.status {
	case StatusCompleted:
		return ErrTaskAlreadyCompleted
	case StatusCancelled:
		return ErrTaskCancelled
	default:
		return ErrInvalidStatusTransition
	}
}

// meta собирает общую часть доменного события.
func (t *Task) meta(now time.Time) eventMeta {
	return eventMeta{taskID: t.id, at: now}
}

// apply фиксирует состоявшуюся мутацию: двигает момент изменения и версию
// агрегата, а событие ставит в очередь на выдачу.
//
// Вызывается последней строкой каждого мутатора — ровно затем, чтобы забыть
// одну из трёх частей было негде.
func (t *Task) apply(now time.Time, event DomainEvent) {
	t.updatedAt = now
	t.version++
	t.events = append(t.events, event)
}

// transitionTo переводит задачу в target, сверяясь с конечным автоматом.
func (t *Task) transitionTo(target Status, now time.Time, event DomainEvent) error {
	if err := t.ensureMutable(); err != nil {
		return err
	}
	if !t.status.CanTransitionTo(target) {
		return ErrInvalidStatusTransition
	}

	t.status = target
	t.apply(now, event)

	return nil
}

// Rename меняет заголовок задачи.
func (t *Task) Rename(title Title, now time.Time) error {
	if err := t.ensureMutable(); err != nil {
		return err
	}
	if title.IsZero() {
		return ErrEmptyTitle
	}

	t.title = title
	t.apply(now, TaskRenamed{eventMeta: t.meta(now), NewTitle: title})

	return nil
}

// Describe меняет описание задачи. Пустое описание допустимо, поэтому
// проверять здесь нечего: Description невалидным не бывает.
func (t *Task) Describe(description Description, now time.Time) error {
	if err := t.ensureMutable(); err != nil {
		return err
	}

	t.description = description
	t.apply(now, TaskDescribed{eventMeta: t.meta(now), NewDescription: description})

	return nil
}

// ChangePriority меняет приоритет задачи.
func (t *Task) ChangePriority(priority Priority, now time.Time) error {
	if err := t.ensureMutable(); err != nil {
		return err
	}
	if !priority.IsValid() {
		return ErrUnknownPriority
	}

	t.priority = priority
	t.apply(now, TaskPriorityChanged{eventMeta: t.meta(now), NewPriority: priority})

	return nil
}

// Reschedule назначает, переносит или снимает срок выполнения.
// Nil снимает срок. Срок, не наступающий в будущем относительно now,
// отвергается даже если он был корректен в момент своего создания.
func (t *Task) Reschedule(dueDate *DueDate, now time.Time) error {
	if err := t.ensureMutable(); err != nil {
		return err
	}
	if dueDate != nil && !dueDate.Time().After(now) {
		return ErrDueDateInPast
	}

	t.dueDate = dueDate
	t.apply(now, TaskRescheduled{eventMeta: t.meta(now), NewDueDate: dueDate})

	return nil
}

// Start переводит задачу в работу.
func (t *Task) Start(now time.Time) error {
	return t.transitionTo(StatusInProgress, now, TaskStarted{eventMeta: t.meta(now)})
}

// Complete отмечает задачу выполненной.
func (t *Task) Complete(now time.Time) error {
	if err := t.transitionTo(StatusCompleted, now, TaskCompleted{eventMeta: t.meta(now)}); err != nil {
		return err
	}

	completedAt := now
	t.completedAt = &completedAt

	return nil
}

// Cancel отменяет задачу.
func (t *Task) Cancel(now time.Time) error {
	return t.transitionTo(StatusCancelled, now, TaskCancelled{eventMeta: t.meta(now)})
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
