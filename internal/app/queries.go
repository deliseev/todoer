package app

import "time"

// Запросы описывают намерение прочитать задачу, а команды — изменить её.
// Разделение не косметическое: чтение не порождает событий, не двигает версию
// и не нуждается в оптимистичной блокировке.

// GetTaskQuery — чтение одной задачи.
type GetTaskQuery struct {
	TaskID  string
	OwnerID string
}

// TaskView — представление задачи для чтения.
//
// Плоское и строковое: агрегат наружу не отдаётся, иначе вызывающий получил
// бы его мутаторы вместе с данными, а транспорт начал бы зависеть от домена.
// Времена остаются time.Time — их формат выбирает тот, кто сериализует.
type TaskView struct {
	ID          string
	OwnerID     string
	Title       string
	Description string
	Status      string
	Priority    string
	DueDate     *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
	Version     int
}
