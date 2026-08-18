package apptest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

// errWorkFailed — отказ, который тест устраивает внутри работы, чтобы
// посмотреть, откатится ли начатое. Свой, а не чужой: набор проверяет, что
// ошибка вызывающего доезжает до него нетронутой.
var errWorkFailed = errors.New("apptest: work failed")

// UnitOfWorkFixture — проверяемая единица работы вместе со средством осмотра.
//
// Очередь событий порт наружу не отдаёт: её читает доставщик, а не сценарий,
// и заводить ради теста метод, которым никто не пользуется, незачем. Поэтому
// заглянуть в неё даёт реализация — своим способом, как ей удобно.
type UnitOfWorkFixture struct {
	// UnitOfWork — проверяемая реализация.
	UnitOfWork app.UnitOfWork
	// QueuedEvents отдаёт имена событий в очереди в порядке записи.
	QueuedEvents func(t *testing.T) []string
}

// UnitOfWorkContract прогоняет по реализации app.UnitOfWork всё, что порт
// обещает вызывающему.
//
// Обещание у него одно, но крупное: работа фиксируется целиком или не
// фиксируется вовсе. Ради него порт и заведён — задача и её события обязаны
// оказаться в хранилище одним движением, иначе outbox не даёт ничего, чего
// не давала синхронная публикация.
//
// newUnitOfWork обязан отдавать пустое хранилище: подтесты друг о друге не
// знают и общего состояния не переживут.
func UnitOfWorkContract(t *testing.T, newUnitOfWork func(t *testing.T) UnitOfWorkFixture) {
	t.Helper()

	contract := []struct {
		name string
		run  func(t *testing.T, fixture UnitOfWorkFixture)
	}{
		{"задача и её события фиксируются вместе", contractCommitsTogether},
		{"изменения видны внутри той же работы", contractReadsOwnWrites},
		{"работа без изменений фиксируется", contractEmptyWork},
		{"пустая партия событий допустима", contractEmptyBatch},

		{"отказ внутри работы не оставляет следов", contractRollsBackOnError},
		{"отказ вызывающего доезжает нетронутым", contractReturnsWorkError},
		{"паника внутри работы откатывает запись", contractRollsBackOnPanic},
		{"конфликт версий доезжает до вызывающего", contractConflictPropagates},

		{"события ложатся в порядке возникновения", contractKeepsEventOrder},
		{"отменённый контекст ничего не пишет", contractRespectsCancel},
	}

	for _, c := range contract {
		t.Run(c.name, func(t *testing.T) {
			fixture := newUnitOfWork(t)
			if fixture.UnitOfWork == nil {
				t.Fatal("newUnitOfWork вернул фикстуру без единицы работы")
			}
			if fixture.QueuedEvents == nil {
				t.Fatal("newUnitOfWork вернул фикстуру без средства осмотра очереди")
			}
			c.run(t, fixture)
		})
	}
}

// contractCommitsTogether: после успешной работы видно и задачу, и её события.
func contractCommitsTogether(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)

	mustDo(t, fixture.UnitOfWork, func(ctx context.Context, tx app.Tx) error {
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}
		return tx.Outbox().Add(ctx, task.PullEvents())
	})

	got := getInWork(t, fixture.UnitOfWork, task.ID())
	if got.Title() != task.Title() {
		t.Errorf("заголовок = %q, ожидался %q", got.Title(), task.Title())
	}

	want := []string{todo.EventTaskCreated}
	if queued := fixture.QueuedEvents(t); !slices.Equal(queued, want) {
		t.Errorf("в очереди %v, ожидалось %v", queued, want)
	}
}

// contractReadsOwnWrites: работа видит то, что сама только что записала.
// Без этого сценарий не смог бы сохранить задачу и тут же её перечитать,
// а изоляция транзакции превратилась бы в ловушку.
func contractReadsOwnWrites(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)

	mustDo(t, fixture.UnitOfWork, func(ctx context.Context, tx app.Tx) error {
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}

		got, version, err := tx.Tasks().Get(ctx, task.ID())
		if err != nil {
			return err
		}
		if got == nil {
			t.Fatal("Get(...) внутри работы вернул nil без ошибки")
		}
		if version != task.Version() {
			t.Errorf("версия = %d, ожидалась %d", version, task.Version())
		}
		return nil
	})
}

// contractEmptyWork: работа, ничего не сделавшая, — не ошибка.
func contractEmptyWork(t *testing.T, fixture UnitOfWorkFixture) {
	mustDo(t, fixture.UnitOfWork, func(context.Context, app.Tx) error {
		return nil
	})
}

// contractEmptyBatch: пустая партия событий принимается молча и ничего не
// кладёт в очередь. Сценарий такого не присылает, но порт обязан быть
// снисходительным: иначе реализация очереди начнёт диктовать, как считать
// «изменение без событий».
func contractEmptyBatch(t *testing.T, fixture UnitOfWorkFixture) {
	mustDo(t, fixture.UnitOfWork, func(ctx context.Context, tx app.Tx) error {
		return tx.Outbox().Add(ctx, nil)
	})

	if queued := fixture.QueuedEvents(t); len(queued) != 0 {
		t.Errorf("в очереди %v, ожидалось пусто", queued)
	}
}

// contractRollsBackOnError: отказ внутри работы отменяет всё, что она успела.
// Половина записи здесь — то же самое, что событие без задачи.
func contractRollsBackOnError(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)

	err := fixture.UnitOfWork.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}
		if err := tx.Outbox().Add(ctx, task.PullEvents()); err != nil {
			return err
		}
		return errWorkFailed
	})
	if err == nil {
		t.Fatal("Do(...) не вернул ошибку отказавшей работы")
	}

	requireNothingStored(t, fixture, task.ID())
}

// contractReturnsWorkError: ошибка вызывающего возвращается как есть —
// по ней он и будет ветвиться.
func contractReturnsWorkError(t *testing.T, fixture UnitOfWorkFixture) {
	err := fixture.UnitOfWork.Do(t.Context(), func(context.Context, app.Tx) error {
		return errWorkFailed
	})

	if !errors.Is(err, errWorkFailed) {
		t.Fatalf("ожидалась ошибка работы, получено: %v", err)
	}
}

// contractRollsBackOnPanic: паника — тоже незавершённая работа, и оставлять
// после неё половину записи нельзя. Реализация обязана откатывать начатое
// на любом выходе, а не только на возврате ошибки.
func contractRollsBackOnPanic(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("паника из работы не долетела до вызывающего")
			}
		}()

		_ = fixture.UnitOfWork.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
			if err := tx.Tasks().Save(ctx, task, 0); err != nil {
				return err
			}
			panic("apptest: работа паникует")
		})
	}()

	requireNothingStored(t, fixture, task.ID())
}

// contractConflictPropagates: оптимистичная блокировка работает и внутри
// работы, а её сентинель доезжает до вызывающего.
func contractConflictPropagates(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)

	err := fixture.UnitOfWork.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		// Версия подъёма от несуществующей задачи: писатель уверен, что поднимал
		// её из хранилища, а её там нет.
		return tx.Tasks().Save(ctx, task, task.Version())
	})

	if !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
	}
	requireNothingStored(t, fixture, task.ID())
}

// contractKeepsEventOrder: события описывают прошлое, и порядок в очереди
// обязан совпадать с порядком, в котором это прошлое случилось.
func contractKeepsEventOrder(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustRename(t, task, "Купить кефир", testLater)
	if err := task.Start(testLater); err != nil {
		t.Fatalf("Start(...) вернул ошибку: %v", err)
	}

	mustDo(t, fixture.UnitOfWork, func(ctx context.Context, tx app.Tx) error {
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}
		return tx.Outbox().Add(ctx, task.PullEvents())
	})

	want := []string{todo.EventTaskCreated, todo.EventTaskRenamed, todo.EventTaskStarted}
	if queued := fixture.QueuedEvents(t); !slices.Equal(queued, want) {
		t.Errorf("в очереди %v, ожидалось %v", queued, want)
	}
}

// contractRespectsCancel: отменённый запрос не записывает ничего. До фиксации
// отмена законна и работу останавливает.
func contractRespectsCancel(t *testing.T, fixture UnitOfWorkFixture) {
	task := todotest.NewTask(t, testOwner, testNow)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := fixture.UnitOfWork.Do(ctx, func(ctx context.Context, tx app.Tx) error {
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}
		return tx.Outbox().Add(ctx, task.PullEvents())
	})
	if err == nil {
		t.Fatal("Do(...) отменённого запроса не вернул ошибку")
	}

	requireNothingStored(t, fixture, task.ID())
}

// requireNothingStored проверяет, что от откаченной работы не осталось ни
// задачи, ни событий: половина записи хуже отсутствующей — она выглядит как
// состоявшееся изменение, о котором никто не узнал, или наоборот.
func requireNothingStored(t *testing.T, fixture UnitOfWorkFixture, id todo.TaskID) {
	t.Helper()

	err := fixture.UnitOfWork.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		if _, _, err := tx.Tasks().Get(ctx, id); !errors.Is(err, app.ErrTaskNotFound) {
			t.Errorf("задача уцелела после отката: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do(...) вернул ошибку: %v", err)
	}

	if queued := fixture.QueuedEvents(t); len(queued) != 0 {
		t.Errorf("в очереди %v, ожидалось пусто", queued)
	}
}

// mustDo выполняет работу или валит тест.
func mustDo(t *testing.T, uow app.UnitOfWork, work func(ctx context.Context, tx app.Tx) error) {
	t.Helper()

	if err := uow.Do(t.Context(), work); err != nil {
		t.Fatalf("Do(...) вернул ошибку: %v", err)
	}
}

// getInWork поднимает задачу внутри отдельной работы или валит тест.
func getInWork(t *testing.T, uow app.UnitOfWork, id todo.TaskID) *todo.Task {
	t.Helper()

	var task *todo.Task
	mustDo(t, uow, func(ctx context.Context, tx app.Tx) error {
		got, _, err := tx.Tasks().Get(ctx, id)
		if err != nil {
			return err
		}
		task = got
		return nil
	})

	if task == nil {
		t.Fatal("Get(...) вернул nil без ошибки")
	}
	return task
}
