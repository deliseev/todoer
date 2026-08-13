package todo

import "time"

// DueDate — срок выполнения задачи. Всегда строго в будущем относительно
// момента создания и всегда нормализован в UTC, чтобы две одинаковые точки
// времени в разных зонах были равны как значения.
type DueDate struct {
	at time.Time
}

// NewDueDate проверяет срок относительно момента now.
// Срок, совпадающий с now или предшествующий ему, отвергается.
func NewDueDate(at, now time.Time) (DueDate, error) {
	utcAt := at.UTC()
	if utcAt.IsZero() || utcAt.Before(now) || utcAt == now {
		return DueDate{}, ErrDueDateInPast
	}
	return DueDate{at: utcAt}, nil
}

// Time возвращает момент наступления срока в UTC.
func (d DueDate) Time() time.Time {
	return d.at.UTC()
}

// IsZero сообщает, что срок не был инициализирован.
func (d DueDate) IsZero() bool {
	return d.at.IsZero()
}

// IsBefore сообщает, что срок наступил строго раньше момента t.
func (d DueDate) IsBefore(t time.Time) bool {
	return d.at.Before(t)
}
