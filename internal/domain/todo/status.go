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

// statusNames — единственный источник правды об именах статусов.
var statusNames = [...]string{
	StatusPending:    "pending",
	StatusInProgress: "in_progress",
	StatusCompleted:  "completed",
	StatusCancelled:  "cancelled",
}

// transitionMatrix — единственный источник правды о переходах.
// Пустой список исходящих переходов означает терминальный статус.
var transitionMatrix = map[Status][]Status{
	StatusPending:    {StatusInProgress, StatusCompleted, StatusCancelled},
	StatusInProgress: {StatusCompleted, StatusCancelled},
	StatusCompleted:  nil,
	StatusCancelled:  nil,
}

// ParseStatus разбирает статус из строки без учёта регистра
// и окружающих пробелов.
func ParseStatus(s string) (Status, error) {
	name := strings.ToLower(strings.TrimSpace(s))

	for i, candidate := range statusNames {
		if candidate != "" && candidate == name {
			return Status(i), nil
		}
	}

	return StatusPending, ErrUnknownStatus
}

// String возвращает каноническое имя статуса в нижнем регистре.
// Для неизвестного значения возвращает "unknown".
func (s Status) String() string {
	if !s.IsValid() {
		return "unknown"
	}
	return statusNames[s]
}

// IsValid сообщает, что значение входит в список допустимых статусов.
func (s Status) IsValid() bool {
	return int(s) < len(statusNames) && statusNames[s] != ""
}

// IsTerminal сообщает, что из этого статуса нет исходящих переходов.
func (s Status) IsTerminal() bool {
	return s.IsValid() && len(transitionMatrix[s]) == 0
}

// CanTransitionTo сообщает, разрешён ли переход в target.
// Переход в собственный статус переходом не считается.
func (s Status) CanTransitionTo(target Status) bool {
	return slices.Contains(transitionMatrix[s], target)
}
