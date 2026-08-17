// Package todotest собирает доменные значения для тестов — те же фабрики
// todo, но с падением теста вместо возвращаемой ошибки.
//
// Пакет обычный, а не тестовый: этими значениями пользуются тесты домена,
// сценариев, обеих реализаций хранилища, набора контракта и транспорта, а из
// файлов с суффиксом _test импортировать нечего. Так же устроены apptest и
// pgtest в этом проекте и httptest в стандартной библиотеке.
//
// Раньше набор был скопирован в каждый из этих пакетов, и правка доменной
// фабрики стоила пяти одинаковых правок. Здесь он один.
//
// Опорные моменты времени сюда не переехали и не должны: «сейчас» приезжает
// параметром, потому что у каждого пакета своя точка отсчёта, а общая
// изменяемая переменная времени — состояние на весь тестовый бинарник.
package todotest

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// MustTitle создаёт заголовок или валит тест.
func MustTitle(t *testing.T, s string) todo.Title {
	t.Helper()

	title, err := todo.NewTitle(s)
	if err != nil {
		t.Fatalf("NewTitle(%q) вернул ошибку: %v", s, err)
	}
	return title
}

// MustDescription создаёт описание или валит тест.
func MustDescription(t *testing.T, s string) todo.Description {
	t.Helper()

	description, err := todo.NewDescription(s)
	if err != nil {
		t.Fatalf("NewDescription(%q) вернул ошибку: %v", s, err)
	}
	return description
}

// MustTaskID создаёт новый идентификатор задачи или валит тест.
func MustTaskID(t *testing.T) todo.TaskID {
	t.Helper()

	id, err := todo.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() вернул ошибку: %v", err)
	}
	return id
}

// MustOwnerID разбирает идентификатор владельца или валит тест.
func MustOwnerID(t *testing.T, s string) todo.OwnerID {
	t.Helper()

	id, err := todo.ParseOwnerID(s)
	if err != nil {
		t.Fatalf("ParseOwnerID(%q) вернул ошибку: %v", s, err)
	}
	return id
}

// MustDueDate создаёт срок выполнения относительно now или валит тест.
//
// Момент «сейчас» приезжает параметром, а не берётся из пакета: правило срока
// задано относительно него, и подставлять сюда чужое «сейчас» значило бы
// проверять не тот срок, который увидит домен.
func MustDueDate(t *testing.T, at, now time.Time) *todo.DueDate {
	t.Helper()

	due, err := todo.NewDueDate(at, now)
	if err != nil {
		t.Fatalf("NewDueDate(%s, %s) вернул ошибку: %v", at, now, err)
	}
	return &due
}

// MustReconstituteDueDate восстанавливает срок из хранилища или валит тест.
//
// В отличие от MustDueDate, «сейчас» ей не нужно: восстановление сроку в
// прошлом не препятствует — просроченная задача законно лежит в базе.
func MustReconstituteDueDate(t *testing.T, at time.Time) todo.DueDate {
	t.Helper()

	due, err := todo.ReconstituteDueDate(at)
	if err != nil {
		t.Fatalf("ReconstituteDueDate(%s) вернул ошибку: %v", at, err)
	}
	return due
}

// NewTask создаёт типовую задачу: обычный приоритет, статус pending, срок
// через сутки после now.
//
// Годится там, где нужна просто какая-нибудь живая задача, — а где важны её
// поля, задачу собирают на месте из фабрик выше.
func NewTask(t *testing.T, owner string, now time.Time) *todo.Task {
	t.Helper()

	task, err := todo.NewTask(
		MustTaskID(t),
		MustOwnerID(t, owner),
		MustTitle(t, "Купить молоко"),
		MustDescription(t, "Два литра, в магазине у дома"),
		todo.PriorityNormal,
		MustDueDate(t, now.Add(24*time.Hour), now),
		now,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}
	return task
}

// EventNames раскладывает события в список имён — так порядок и состав
// читаются в диагностике одной строкой.
func EventNames(events []todo.DomainEvent) []string {
	names := make([]string, len(events))
	for i, e := range events {
		names[i] = e.EventName()
	}
	return names
}
