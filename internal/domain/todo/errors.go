// Package todo содержит доменную модель управления задачами.
//
// Пакет намеренно не знает ничего о хранилищах, транспорте и фреймворках:
// это чистое ядро, вокруг которого позже вырастут application-слой,
// инфраструктура и отдельные микросервисы.
package todo

import "errors"

// Сентинельные доменные ошибки. Вызывающая сторона сверяет их через errors.Is,
// поэтому обёртки обязаны использовать %w.
var (
	// ErrEmptyTitle — заголовок пуст или состоит только из пробельных символов.
	ErrEmptyTitle = errors.New("todo: title is empty")
	// ErrTitleTooLong — длина заголовка превышает MaxTitleLength рун.
	ErrTitleTooLong = errors.New("todo: title is too long")
	// ErrDescriptionTooLong — длина описания превышает MaxDescriptionLength рун.
	ErrDescriptionTooLong = errors.New("todo: description is too long")

	// ErrInvalidTaskID — идентификатор задачи пуст или имеет неверный формат.
	ErrInvalidTaskID = errors.New("todo: invalid task id")
	// ErrInvalidOwnerID — идентификатор владельца пуст или имеет неверный формат.
	ErrInvalidOwnerID = errors.New("todo: invalid owner id")

	// ErrDueDateInPast — срок выполнения не наступает в будущем.
	ErrDueDateInPast = errors.New("todo: due date is in the past")
	// ErrInvalidDueDate — срок не описывает момент времени.
	ErrInvalidDueDate = errors.New("todo: invalid due date")

	// ErrUnknownPriority — строка не соответствует ни одному приоритету.
	ErrUnknownPriority = errors.New("todo: unknown priority")
	// ErrUnknownStatus — строка не соответствует ни одному статусу.
	ErrUnknownStatus = errors.New("todo: unknown status")

	// ErrInvalidStatusTransition — переход между статусами запрещён автоматом.
	ErrInvalidStatusTransition = errors.New("todo: invalid status transition")
	// ErrTaskAlreadyCompleted — задача уже выполнена, изменять её нельзя.
	ErrTaskAlreadyCompleted = errors.New("todo: task is already completed")
	// ErrTaskCancelled — задача отменена, изменять её нельзя.
	ErrTaskCancelled = errors.New("todo: task is cancelled")
)
