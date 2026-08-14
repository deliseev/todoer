package infra_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/infra"
)

// Хранилище в памяти обязано удовлетворять порту app.Repository.
var _ app.Repository = (*infra.InMemoryTaskRepository)(nil)

func TestInMemoryTaskRepositorySaveAndGet(t *testing.T) {
	t.Run("новая задача поднимается целиком", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)

		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got == nil {
			t.Fatal("Get(...) вернул nil без ошибки")
		}

		want := task.Snapshot()
		have := got.Snapshot()

		if have.ID != want.ID {
			t.Errorf("id = %s, ожидался %s", have.ID, want.ID)
		}
		if have.OwnerID != want.OwnerID {
			t.Errorf("владелец = %s, ожидался %s", have.OwnerID, want.OwnerID)
		}
		if have.Title != want.Title {
			t.Errorf("заголовок = %q, ожидался %q", have.Title, want.Title)
		}
		if have.Description != want.Description {
			t.Errorf("описание = %q, ожидалось %q", have.Description, want.Description)
		}
		if have.Status != want.Status {
			t.Errorf("статус = %s, ожидался %s", have.Status, want.Status)
		}
		if have.Priority != want.Priority {
			t.Errorf("приоритет = %s, ожидался %s", have.Priority, want.Priority)
		}
		if have.Version != want.Version {
			t.Errorf("версия = %d, ожидалась %d", have.Version, want.Version)
		}
		if have.DueDate == nil {
			t.Fatal("срок потерян")
		}
		// Моменты времени сравниваются только через Equal: == у time.Time
		// сличает внутреннее представление, а не момент.
		if !have.DueDate.Time().Equal(want.DueDate.Time()) {
			t.Errorf("срок = %s, ожидался %s", have.DueDate.Time(), want.DueDate.Time())
		}
		if !have.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("createdAt = %s, ожидалось %s", have.CreatedAt, want.CreatedAt)
		}
		if !have.UpdatedAt.Equal(want.UpdatedAt) {
			t.Errorf("updatedAt = %s, ожидалось %s", have.UpdatedAt, want.UpdatedAt)
		}
	})

	t.Run("задача без срока поднимается без срока", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()

		task, err := todo.NewTask(
			mustTaskID(t),
			mustOwnerID(t, testOwner),
			mustTitle(t, "Позвонить в сервис"),
			mustDescription(t, ""),
			todo.PriorityHigh,
			nil,
			testNow,
		)
		if err != nil {
			t.Fatalf("NewTask(...) вернул ошибку: %v", err)
		}
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if _, ok := got.DueDate(); ok {
			t.Error("у задачи появился срок, которого не было")
		}
	})

	t.Run("выполненная задача сохраняет момент завершения", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := task.Complete(testLater); err != nil {
			t.Fatalf("Complete(...) вернул ошибку: %v", err)
		}

		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}

		completedAt, ok := got.CompletedAt()
		if !ok {
			t.Fatal("момент завершения потерян")
		}
		if !completedAt.Equal(testLater) {
			t.Errorf("completedAt = %s, ожидалось %s", completedAt, testLater)
		}
		if got.Status() != todo.StatusCompleted {
			t.Errorf("статус = %s, ожидался %s", got.Status(), todo.StatusCompleted)
		}
	})

	t.Run("задачи не путаются между собой", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		first, second := newTestTask(t), newTestTask(t)
		mustRename(t, second, "Купить кефир", testLater)

		for _, task := range []*todo.Task{first, second} {
			if err := repo.Save(t.Context(), task); err != nil {
				t.Fatalf("Save(...) вернул ошибку: %v", err)
			}
		}

		got, err := repo.Get(t.Context(), first.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got.Title() != first.Title() {
			t.Errorf("заголовок = %q, ожидался %q", got.Title(), first.Title())
		}
	})
}

func TestInMemoryTaskRepositoryGetMissing(t *testing.T) {
	t.Run("несуществующая задача", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()

		got, err := repo.Get(t.Context(), mustTaskID(t))
		if !errors.Is(err, app.ErrTaskNotFound) {
			t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
		}
		// Порт не должен отдавать пару (nil, nil) или задачу вместе с
		// ошибкой: вызывающий разыменует её сразу после проверки err.
		if got != nil {
			t.Error("вместе с ошибкой возвращена задача")
		}
	})
}

func TestInMemoryTaskRepositoryUpdate(t *testing.T) {
	t.Run("обновление заменяет состояние", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		stored, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		mustRename(t, stored, "Купить кефир", testLater)
		if err := repo.Save(t.Context(), stored); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got.Title().String() != "Купить кефир" {
			t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить кефир")
		}
		if got.Version() != task.Version()+1 {
			t.Errorf("версия = %d, ожидалась %d", got.Version(), task.Version()+1)
		}
		if !got.UpdatedAt().Equal(testLater) {
			t.Errorf("updatedAt = %s, ожидалось %s", got.UpdatedAt(), testLater)
		}
	})
}

func TestInMemoryTaskRepositoryOptimisticLocking(t *testing.T) {
	// Обе копии подняты из одного состояния — это и есть два параллельных
	// редактора, каждый со своей версией правды.
	saveTwoWriters := func(t *testing.T) (*infra.InMemoryTaskRepository, *todo.Task, *todo.Task) {
		t.Helper()

		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		first, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		second, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		return repo, first, second
	}

	t.Run("второй редактор получает конфликт", func(t *testing.T) {
		repo, first, second := saveTwoWriters(t)

		mustRename(t, first, "Купить кефир", testLater)
		if err := repo.Save(t.Context(), first); err != nil {
			t.Fatalf("Save(...) первого редактора вернул ошибку: %v", err)
		}

		mustRename(t, second, "Купить ряженку", testLater)
		if err := repo.Save(t.Context(), second); !errors.Is(err, app.ErrVersionConflict) {
			t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
		}
	})

	t.Run("конфликт не меняет хранилище", func(t *testing.T) {
		repo, first, second := saveTwoWriters(t)

		mustRename(t, first, "Купить кефир", testLater)
		if err := repo.Save(t.Context(), first); err != nil {
			t.Fatalf("Save(...) первого редактора вернул ошибку: %v", err)
		}

		mustRename(t, second, "Купить ряженку", testLater)
		_ = repo.Save(t.Context(), second)

		got, err := repo.Get(t.Context(), first.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got.Title().String() != "Купить кефир" {
			t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить кефир")
		}
		if got.Version() != first.Version() {
			t.Errorf("версия = %d, ожидалась %d", got.Version(), first.Version())
		}
	})

	t.Run("разрыв версий отвергается", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		// Задача успела уйти вперёд на три версии, минуя хранилище: значит
		// поднимали её не отсюда, и принимать такую запись нельзя — между
		// сохранённым и записываемым состоянием потерялись две мутации.
		stale, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		for _, title := range []string{"Купить кефир", "Купить ряженку", "Купить простоквашу"} {
			mustRename(t, stale, title, testLater)
		}

		if err := repo.Save(t.Context(), stale); !errors.Is(err, app.ErrVersionConflict) {
			t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
		}
	})

	t.Run("повторное сохранение той же версии отвергается", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		// Версия не выросла — значит с прошлой записи ничего не произошло,
		// и это не обновление, а повтор.
		if err := repo.Save(t.Context(), task); !errors.Is(err, app.ErrVersionConflict) {
			t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
		}
	})
}

func TestInMemoryTaskRepositoryInsertRules(t *testing.T) {
	t.Run("задача первой версии вставляется", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)

		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}
	})

	t.Run("задача, изменённая до первой записи, вставляется", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)

		// Агрегат не обязан сохраняться после каждой мутации: «создать,
		// начать, записать один раз» — законный сценарий, и запрещать его
		// хранилище не вправе.
		mustRename(t, task, "Купить кефир", testLater)
		if err := task.Start(testLater); err != nil {
			t.Fatalf("Start(...) вернул ошибку: %v", err)
		}

		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got.Version() != task.Version() {
			t.Errorf("версия = %d, ожидалась %d", got.Version(), task.Version())
		}
		if got.Status() != todo.StatusInProgress {
			t.Errorf("статус = %s, ожидался %s", got.Status(), todo.StatusInProgress)
		}
	})
}

func TestInMemoryTaskRepositoryIsolation(t *testing.T) {
	t.Run("мутация поднятой задачи не видна без Save", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		first, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		mustRename(t, first, "Купить кефир", testLater)

		second, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if second.Title() != task.Title() {
			t.Errorf("заголовок = %q, ожидался %q", second.Title(), task.Title())
		}
		if second.Version() != task.Version() {
			t.Errorf("версия = %d, ожидалась %d", second.Version(), task.Version())
		}
	})

	t.Run("мутация сохранённой задачи не видна хранилищу", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		// Тот же объект, что уже отдали в Save, продолжает жить у вызывающего.
		mustRename(t, task, "Купить кефир", testLater)

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got.Title().String() != "Купить молоко" {
			t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить молоко")
		}
	})

	t.Run("необязательные поля не разделяются указателем", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := task.Complete(testLater); err != nil {
			t.Fatalf("Complete(...) вернул ошибку: %v", err)
		}
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		first, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		second, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}

		// Присваивание структуры в Go копирует указатель, а не значение:
		// две поднятые задачи, смотрящие на один *DueDate, дают доступ
		// к состоянию друг друга в обход методов.
		if first.Snapshot().DueDate == second.Snapshot().DueDate {
			t.Error("срок разделяется указателем между копиями")
		}
		if first.Snapshot().CompletedAt == second.Snapshot().CompletedAt {
			t.Error("момент завершения разделяется указателем между копиями")
		}
	})
}

func TestInMemoryTaskRepositoryRespectsContext(t *testing.T) {
	t.Run("Get отменённого запроса", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if _, err := repo.Get(ctx, task.ID()); !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась context.Canceled, получено: %v", err)
		}
	})

	t.Run("Save отменённого запроса ничего не пишет", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if err := repo.Save(ctx, task); !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась context.Canceled, получено: %v", err)
		}
		if _, err := repo.Get(t.Context(), task.ID()); !errors.Is(err, app.ErrTaskNotFound) {
			t.Errorf("отменённый запрос всё-таки записал задачу: %v", err)
		}
	})
}

func TestInMemoryTaskRepositoryRejectsNilTask(t *testing.T) {
	t.Run("вместо задачи передали nil", func(t *testing.T) {
		repo := infra.NewInMemoryTaskRepository()

		// Ошибка, а не паника: хранилище — граница, и мусор с той стороны
		// оно обязано пережить.
		if err := repo.Save(t.Context(), nil); err == nil {
			t.Fatal("Save(nil) не вернул ошибку")
		}
	})
}

func TestInMemoryTaskRepositoryConcurrentSave(t *testing.T) {
	t.Run("из параллельных редакторов побеждает ровно один", func(t *testing.T) {
		const writers = 8

		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		// Каждый редактор поднимает задачу заранее: все стартуют с одной
		// и той же версии, как два человека, открывшие одну карточку.
		writersTasks := make([]*todo.Task, writers)
		for i := range writersTasks {
			got, err := repo.Get(t.Context(), task.ID())
			if err != nil {
				t.Fatalf("Get(...) вернул ошибку: %v", err)
			}
			mustRename(t, got, "Купить кефир", testLater)
			writersTasks[i] = got
		}

		var succeeded, conflicted atomic.Int64

		var wg sync.WaitGroup
		wg.Add(writers)
		for _, writerTask := range writersTasks {
			go func() {
				defer wg.Done()

				switch err := repo.Save(t.Context(), writerTask); {
				case err == nil:
					succeeded.Add(1)
				case errors.Is(err, app.ErrVersionConflict):
					conflicted.Add(1)
				default:
					t.Errorf("Save(...) вернул неожиданную ошибку: %v", err)
				}
			}()
		}
		wg.Wait()

		if succeeded.Load() != 1 {
			t.Errorf("успешных записей %d, ожидалась 1", succeeded.Load())
		}
		if conflicted.Load() != writers-1 {
			t.Errorf("конфликтов %d, ожидалось %d", conflicted.Load(), writers-1)
		}

		got, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		if got.Version() != task.Version()+1 {
			t.Errorf("версия = %d, ожидалась %d", got.Version(), task.Version()+1)
		}
	})

	t.Run("чтение и запись идут параллельно без гонок", func(t *testing.T) {
		const readers = 8

		repo := infra.NewInMemoryTaskRepository()
		task := newTestTask(t)
		if err := repo.Save(t.Context(), task); err != nil {
			t.Fatalf("Save(...) вернул ошибку: %v", err)
		}

		writer, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		mustRename(t, writer, "Купить кефир", testLater)

		var wg sync.WaitGroup
		wg.Add(readers + 1)

		go func() {
			defer wg.Done()

			if err := repo.Save(t.Context(), writer); err != nil {
				t.Errorf("Save(...) вернул ошибку: %v", err)
			}
		}()

		for range readers {
			go func() {
				defer wg.Done()

				if _, err := repo.Get(t.Context(), task.ID()); err != nil {
					t.Errorf("Get(...) вернул ошибку: %v", err)
				}
			}()
		}
		wg.Wait()
	})
}
