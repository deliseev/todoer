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

// priorityRanks — единственный источник правды о порядке важности.
//
// Отдельная таблица нужна потому, что порядок объявления констант ему не
// годится: нулевым значением обязан быть PriorityNormal — задача без явного
// приоритета обычная, — и из-за этого PriorityLow больше обычного по числу,
// будучи ниже по важности. Имя не годится тем более: по алфавиту critical
// идёт раньше high.
//
// Ранг растёт вместе с важностью и начинается с единицы: ноль означает
// «ранга нет». Приоритет, добавленный в priorityNames и забытый здесь, обязан
// отвечать невозможным рангом, а не молча делить место с самым низким.
var priorityRanks = [...]int{
	PriorityLow:      1,
	PriorityNormal:   2,
	PriorityHigh:     3,
	PriorityCritical: 4,
}

// unknownRank — ответ Rank для значения вне перечисления. Отрицательный,
// а не нулевой: ноль слился бы с законным рангом, и неизвестный приоритет
// сел бы в сортировке на чужое место.
const unknownRank = -1

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

// Rank возвращает место приоритета в порядке важности: чем больше, тем важнее.
// Для значения вне перечисления возвращает unknownRank.
//
// Сравнивать ранги можно, полагаться на конкретные числа — нет: они деталь
// таблицы, и новый приоритет между существующими их подвинет.
func (p Priority) Rank() int {
	if !p.IsValid() || int(p) >= len(priorityRanks) || priorityRanks[p] == 0 {
		return unknownRank
	}
	return priorityRanks[p]
}
