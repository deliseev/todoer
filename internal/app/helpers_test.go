package app_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// Опорные моменты времени. Часы в сценариях подставные, поэтому «сейчас»
// здесь — такая же константа, как в тестах домена.
var (
	testNow   = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	testLater = testNow.Add(time.Hour)
	testDue   = testNow.Add(24 * time.Hour)
)

// testOwner — владелец, от имени которого идут успешные сценарии.
const testOwner = "user-42"

// otherOwner — посторонний, которому чужие задачи трогать нельзя.
const otherOwner = "user-13"

// testEnv — сервис вместе со своими подставными зависимостями.
type testEnv struct {
	service   *app.TaskService
	repo      *fakeRepository
	publisher *recordingPublisher
	clock     *stubClock
}

// newTestEnv собирает сервис на подставных зависимостях с часами на testNow.
func newTestEnv(t *testing.T) testEnv {
	t.Helper()

	repo := newFakeRepository()
	publisher := &recordingPublisher{}
	clock := &stubClock{at: testNow}

	service, err := app.NewTaskService(repo, publisher, clock)
	if err != nil {
		t.Fatalf("NewTaskService(...) вернул ошибку: %v", err)
	}

	return testEnv{service: service, repo: repo, publisher: publisher, clock: clock}
}

// seedTask кладёт в хранилище задачу указанного владельца, доведённую до
// нужного статуса. События подготовки не доходят до публикатора: тест должен
// видеть только то, что породил проверяемый сценарий.
func seedTask(t *testing.T, repo *fakeRepository, owner string, status todo.Status) *todo.Task {
	t.Helper()

	task, err := todo.NewTask(
		mustTaskID(t),
		mustOwnerID(t, owner),
		mustTitle(t, "Купить молоко"),
		mustDescription(t, "Два литра, в магазине у дома"),
		todo.PriorityNormal,
		mustDueDate(t, testDue),
		testNow,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}

	switch status {
	case todo.StatusPending:
		// Только что созданная задача уже ждёт выполнения.
	case todo.StatusInProgress:
		err = task.Start(testNow)
	case todo.StatusCompleted:
		err = task.Complete(testNow)
	case todo.StatusCancelled:
		err = task.Cancel(testNow)
	default:
		t.Fatalf("seedTask: неизвестный статус %v", status)
	}
	if err != nil {
		t.Fatalf("перевод задачи в статус %s вернул ошибку: %v", status, err)
	}

	task.PullEvents()
	repo.put(task.Snapshot())

	return task
}

// eventNames раскладывает события в список имён — так порядок и состав
// читаются в диагностике одной строкой.
func eventNames(events []todo.DomainEvent) []string {
	names := make([]string, len(events))
	for i, e := range events {
		names[i] = e.EventName()
	}
	return names
}

// mustTitle создаёт заголовок или валит тест.
func mustTitle(t *testing.T, s string) todo.Title {
	t.Helper()

	title, err := todo.NewTitle(s)
	if err != nil {
		t.Fatalf("NewTitle(%q) вернул ошибку: %v", s, err)
	}
	return title
}

// mustDescription создаёт описание или валит тест.
func mustDescription(t *testing.T, s string) todo.Description {
	t.Helper()

	description, err := todo.NewDescription(s)
	if err != nil {
		t.Fatalf("NewDescription(%q) вернул ошибку: %v", s, err)
	}
	return description
}

// mustTaskID создаёт новый идентификатор задачи или валит тест.
func mustTaskID(t *testing.T) todo.TaskID {
	t.Helper()

	id, err := todo.NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() вернул ошибку: %v", err)
	}
	return id
}

// mustOwnerID разбирает идентификатор владельца или валит тест.
func mustOwnerID(t *testing.T, s string) todo.OwnerID {
	t.Helper()

	id, err := todo.ParseOwnerID(s)
	if err != nil {
		t.Fatalf("ParseOwnerID(%q) вернул ошибку: %v", s, err)
	}
	return id
}

// mustDueDate создаёт срок выполнения относительно testNow или валит тест.
func mustDueDate(t *testing.T, at time.Time) *todo.DueDate {
	t.Helper()

	due, err := todo.NewDueDate(at, testNow)
	if err != nil {
		t.Fatalf("NewDueDate(%s, %s) вернул ошибку: %v", at, testNow, err)
	}
	return &due
}
