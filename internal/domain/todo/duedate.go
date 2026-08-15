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
	// Сравнивать моменты времени можно только методами After/Before/Equal.
	// Оператор == у time.Time сличает не момент, а внутреннее представление:
	// стенные часы, монотонный отсчёт и указатель на зону. Два одинаковых
	// мгновения в разных зонах для него не равны.
	if !at.After(now) {
		return DueDate{}, ErrDueDateInPast
	}
	return DueDate{at: at.UTC()}, nil
}

// Time возвращает момент наступления срока в UTC.
func (d DueDate) Time() time.Time {
	return d.at
}

// Equal сравнивает сроки как моменты времени.
//
// Через time.Equal, а не через == у структуры: оператор сличает у time.Time
// внутреннее представление — стенные часы, монотонный отсчёт и указатель
// на зону. Нормализация в UTC при создании делает == почти всегда верным,
// и «почти» здесь опаснее явной ошибки.
func (d DueDate) Equal(other DueDate) bool {
	return d.at.Equal(other.at)
}

// IsZero сообщает, что срок не был инициализирован.
func (d DueDate) IsZero() bool {
	return d.at.IsZero()
}

// IsBefore сообщает, что срок наступил строго раньше момента t.
func (d DueDate) IsBefore(t time.Time) bool {
	return d.at.Before(t)
}
