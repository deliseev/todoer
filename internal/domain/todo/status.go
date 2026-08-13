package todo

import (
	"slices"
	"strings"
)

// Status — состояние задачи в её жизненном цикле.
//
// Нулевое значение осмысленно и означает StatusPending: только что созданная
// задача ждёт выполнения.
//
//	Pending ──Start──▶ InProgress ──Complete──▶ Completed (терминальный)
//	   │                    │
//	   ├──Complete──────────┤
//	   └──Cancel────────────┴──Cancel─────────▶ Cancelled (терминальный)
type Status uint8

// Допустимые статусы.
const (
	StatusPending Status = iota
	StatusInProgress
	StatusCompleted
	StatusCancelled
)

const (
	pending    = "pending"
	inProgress = "in_progress"
	completed  = "completed"
	cancelled  = "cancelled"
)

var transitionMatrix = map[Status][]Status{
	StatusPending: []Status{StatusInProgress,
		StatusCompleted,
		StatusCancelled},
	StatusInProgress: []Status{StatusCompleted, StatusCancelled},
	StatusCompleted:  nil,
	StatusCancelled:  nil,
}

// ParseStatus разбирает статус из строки без учёта регистра
// и окружающих пробелов.
func ParseStatus(s string) (Status, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case pending:
		return StatusPending, nil
	case inProgress:
		return StatusInProgress, nil
	case completed:
		return StatusCompleted, nil
	case cancelled:
		return StatusCancelled, nil
	}
	return Status(0), ErrUnknownStatus
}

// String возвращает каноническое имя статуса в нижнем регистре.
// Для неизвестного значения возвращает "unknown".
func (s Status) String() string {
	switch s {
	case StatusPending:
		return pending
	case StatusInProgress:
		return inProgress
	case StatusCompleted:
		return completed
	case StatusCancelled:
		return cancelled
	}
	return unknown
}

// IsValid сообщает, что значение входит в список допустимых статусов.
func (s Status) IsValid() bool {
	return s.String() != unknown
}

// IsTerminal сообщает, что из этого статуса нет исходящих переходов.
func (s Status) IsTerminal() bool {
	allowed, ok := transitionMatrix[s]
	return ok && allowed == nil
}

// CanTransitionTo сообщает, разрешён ли переход в target.
// Переход в собственный статус переходом не считается.
func (s Status) CanTransitionTo(target Status) bool {
	allowed := transitionMatrix[s]
	if slices.Contains(allowed, target) {
		return true
	}
	return false
}
