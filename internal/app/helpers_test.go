package app_test

import (
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
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
	service *app.TaskService
	repo    *fakeRepository
	outbox  *fakeOutbox
	keys    *fakeKeys
	queries *fakeQueries
	clock   *stubClock
}

// newTestEnv собирает сервис на подставных зависимостях с часами на testNow.
//
// Хранилище одно и то же по обе стороны: изменения идут через единицу работы,
// чтения — мимо неё, но видят они одно и то же состояние.
func newTestEnv(t *testing.T) testEnv {
	t.Helper()

	repo := newFakeRepository()
	outbox := newFakeOutbox()
	clock := &stubClock{at: testNow}
	uow := newFakeUnitOfWork(repo, outbox)
	queries := newFakeQueries()

	service, err := app.NewTaskService(uow, repo, queries, clock)
	if err != nil {
		t.Fatalf("NewTaskService(...) вернул ошибку: %v", err)
	}

	return testEnv{
		service: service,
		repo:    repo,
		outbox:  outbox,
		keys:    uow.keys,
		queries: queries,
		clock:   clock,
	}
}

// seedTask кладёт в хранилище задачу указанного владельца, доведённую до
// нужного статуса. События подготовки не доходят до публикатора: тест должен
// видеть только то, что породил проверяемый сценарий.
func seedTask(t *testing.T, repo *fakeRepository, owner string, status todo.Status) *todo.Task {
	t.Helper()

	task := todotest.NewTask(t, owner, testNow)

	var err error
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
