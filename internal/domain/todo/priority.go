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

const (
	normal   = "normal"
	low      = "low"
	high     = "high"
	critical = "critical"
	unknown  = "unknown"
)

// ParsePriority разбирает приоритет из строки без учёта регистра
// и окружающих пробелов.
func ParsePriority(s string) (Priority, error) {
	var p Priority
	switch strings.ToLower(strings.TrimSpace(s)) {
	case normal:
		p = PriorityNormal
	case low:
		p = PriorityLow
	case high:
		p = PriorityHigh
	case critical:
		p = PriorityCritical
	default:
		return Priority(0), ErrUnknownPriority
	}
	return p, nil
}

// String возвращает каноническое имя приоритета в нижнем регистре.
// Для неизвестного значения возвращает "unknown".
func (p Priority) String() string {
	var s string
	switch p {
	case PriorityNormal:
		s = normal
	case PriorityLow:
		s = low
	case PriorityHigh:
		s = high
	case PriorityCritical:
		s = critical
	default:
		s = unknown
	}
	return s
}

// IsValid сообщает, что значение входит в список допустимых приоритетов.
func (p Priority) IsValid() bool {
	return p.String() != "unknown"
}
