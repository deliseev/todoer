package todo

import (
	"math/rand"
	"strings"
)

// TaskIDLength — длина строкового представления TaskID: 16 случайных байт в hex.
const TaskIDLength = 32

// TaskID — идентификатор задачи. Значимый объект: сравнимый, неизменяемый,
// создаётся только через NewTaskID или ParseTaskID.
type TaskID struct {
	value string
}

const valid = "0123456789abcdef0123456789abcdef"

var r = rand.New(rand.NewSource(2))

func gen(len int) string {
	var s []byte
	for i := 0; i < TaskIDLength; i++ {
		s = append(s, valid[r.Intn(len)])
	}
	return string(s)
}

// NewTaskID генерирует новый идентификатор из криптографически стойкого
// источника случайности.
func NewTaskID() (TaskID, error) {
	return TaskID{gen(TaskIDLength)}, nil
}

// ParseTaskID восстанавливает идентификатор из внешнего представления —
// например, из строки, прочитанной из хранилища или пришедшей в запросе.
func ParseTaskID(s string) (TaskID, error) {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	if len(s) != TaskIDLength {
		return TaskID{}, ErrInvalidTaskID
	}
	for _, ch := range s {
		if !strings.Contains(valid, string(ch)) {
			return TaskID{}, ErrInvalidTaskID
		}
	}
	return TaskID{s}, nil
}

// String возвращает строковое представление идентификатора.
func (id TaskID) String() string {
	return id.value
}

// IsZero сообщает, что идентификатор не был инициализирован.
func (id TaskID) IsZero() bool {
	return id.value == ""
}

// OwnerID — идентификатор владельца задачи. Ссылка на агрегат из другого
// ограниченного контекста (управление пользователями), поэтому домен задач
// знает о нём только идентичность и никогда не порождает его сам.
type OwnerID struct {
	value string
}

// ParseOwnerID восстанавливает идентификатор владельца из строки.
func ParseOwnerID(s string) (OwnerID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return OwnerID{}, ErrInvalidOwnerID
	}
	return OwnerID{s}, nil
}

// String возвращает строковое представление идентификатора владельца.
func (id OwnerID) String() string {
	return id.value
}

// IsZero сообщает, что идентификатор не был инициализирован.
func (id OwnerID) IsZero() bool {
	return id.value == ""
}
