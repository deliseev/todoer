package todo

import "strings"

// Priority — важность задачи.
//
// Нулевое значение осмысленно и означает PriorityNormal: задача, созданная
// без явного указания приоритета, считается обычной.
type Priority uint8

// Допустимые приоритеты.
const (
	PriorityNormal Priority = iota
	PriorityLow
	PriorityHigh
	PriorityCritical
)

// priorityNames — единственный источник правды об именах приоритетов.
// И String, и ParsePriority читают эту таблицу, поэтому разойтись им негде,
// а новый приоритет добавляется одной строкой.
var priorityNames = [...]string{
	PriorityNormal:   "normal",
	PriorityLow:      "low",
	PriorityHigh:     "high",
	PriorityCritical: "critical",
}

// ParsePriority разбирает приоритет из строки без учёта регистра
// и окружающих пробелов.
func ParsePriority(s string) (Priority, error) {
	name := strings.ToLower(strings.TrimSpace(s))

	for i, candidate := range priorityNames {
		if candidate != "" && candidate == name {
			return Priority(i), nil
		}
	}

	return PriorityNormal, ErrUnknownPriority
}

// String возвращает каноническое имя приоритета в нижнем регистре.
// Для неизвестного значения возвращает "unknown".
func (p Priority) String() string {
	if !p.IsValid() {
		return "unknown"
	}
	return priorityNames[p]
}

// IsValid сообщает, что значение входит в список допустимых приоритетов.
func (p Priority) IsValid() bool {
	return int(p) < len(priorityNames) && priorityNames[p] != ""
}
