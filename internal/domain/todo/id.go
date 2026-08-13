package todo

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// taskIDBytes — размер идентификатора до hex-кодирования. 16 байт (128 бит)
// дают ту же вероятность коллизии, что и UUIDv4, без внешних зависимостей.
const taskIDBytes = 16

// TaskIDLength — длина строкового представления TaskID: taskIDBytes в hex.
const TaskIDLength = taskIDBytes * 2

// TaskID — идентификатор задачи. Значимый объект: сравнимый, неизменяемый,
// создаётся только через NewTaskID или ParseTaskID.
type TaskID struct {
	value string
}

// NewTaskID генерирует новый идентификатор из криптографически стойкого
// источника случайности. Безопасен для вызова из нескольких горутин.
func NewTaskID() (TaskID, error) {
	var buf [taskIDBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return TaskID{}, fmt.Errorf("todo: сгенерировать идентификатор задачи: %w", err)
	}
	return TaskID{value: hex.EncodeToString(buf[:])}, nil
}

// ParseTaskID восстанавливает идентификатор из внешнего представления —
// например, из строки, прочитанной из хранилища или пришедшей в запросе.
func ParseTaskID(s string) (TaskID, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	if len(s) != TaskIDLength {
		return TaskID{}, fmt.Errorf("%w: длина %d, ожидалось %d", ErrInvalidTaskID, len(s), TaskIDLength)
	}
	if _, err := hex.DecodeString(s); err != nil {
		return TaskID{}, fmt.Errorf("%w: %w", ErrInvalidTaskID, err)
	}

	return TaskID{value: s}, nil
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
// Формат непрозрачен: чужой контекст волен выдавать что угодно непустое.
func ParseOwnerID(s string) (OwnerID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return OwnerID{}, ErrInvalidOwnerID
	}
	return OwnerID{value: s}, nil
}

// String возвращает строковое представление идентификатора владельца.
func (id OwnerID) String() string {
	return id.value
}

// IsZero сообщает, что идентификатор не был инициализирован.
func (id OwnerID) IsZero() bool {
	return id.value == ""
}
