package app

import "time"

// Команды описывают намерение изменить задачу во внешнем, ещё не разобранном
// виде: строки и указатели на время, ровно то, что приходит из транспорта.
// Превращение их в значимые объекты домена — работа сценария, поэтому
// транспортный слой остаётся тупым и о домене не знает.

// CreateTaskCommand — создание задачи.
type CreateTaskCommand struct {
	OwnerID     string
	Title       string
	Description string
	// Priority — имя приоритета. Пустая строка означает обычный приоритет:
	// нулевое значение todo.Priority осмысленно, и заставлять вызывающего
	// писать "normal" незачем.
	Priority string
	// DueDate — срок выполнения. Nil означает задачу без срока.
	DueDate *time.Time
}

// RenameTaskCommand — смена заголовка.
type RenameTaskCommand struct {
	TaskID  string
	OwnerID string
	Title   string
}

// DescribeTaskCommand — смена описания. Пустое описание допустимо.
type DescribeTaskCommand struct {
	TaskID      string
	OwnerID     string
	Description string
}

// ChangePriorityCommand — смена приоритета.
type ChangePriorityCommand struct {
	TaskID   string
	OwnerID  string
	Priority string
}

// RescheduleTaskCommand — назначение, перенос или снятие срока.
// Nil в DueDate снимает срок.
type RescheduleTaskCommand struct {
	TaskID  string
	OwnerID string
	DueDate *time.Time
}

// StartTaskCommand — перевод задачи в работу.
type StartTaskCommand struct {
	TaskID  string
	OwnerID string
}

// CompleteTaskCommand — отметка о выполнении.
type CompleteTaskCommand struct {
	TaskID  string
	OwnerID string
}

// CancelTaskCommand — отмена задачи.
type CancelTaskCommand struct {
	TaskID  string
	OwnerID string
}
