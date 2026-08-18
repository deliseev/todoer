package apptest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

// IdempotencyContract прогоняет по реализации app.IdempotencyStore всё, что
// порт обещает вызывающему.
//
// Обещание тут одно и держится на замке: за одним ключом одного владельца
// стоит ровно одна задача, сколько бы запросов с ним ни пришло — подряд или
// одновременно. Проверять это на двойнике мало: настоящую гонку двух
// вставок разрешает база, и до неё набор добирается только здесь.
func IdempotencyContract(t *testing.T, newUnitOfWork func(t *testing.T) app.UnitOfWork) {
	t.Helper()

	contract := []struct {
		name string
		run  func(t *testing.T, uow app.UnitOfWork)
	}{
		{"первый запрос занимает ключ", contractKeyReserved},
		{"повтор возвращает прежнюю задачу", contractKeyReplayed},
		{"ключ живёт в пределах владельца", contractKeyScopedToOwner},
		{"тот же ключ с другим запросом отвергается", contractKeyReuseRejected},
		{"откат работы освобождает ключ", contractKeyRolledBack},
		{"из параллельных повторов побеждает один", contractConcurrentReplays},
	}

	for _, c := range contract {
		t.Run(c.name, func(t *testing.T) {
			uow := newUnitOfWork(t)
			if uow == nil {
				t.Fatal("newUnitOfWork вернул nil")
			}
			c.run(t, uow)
		})
	}
}

// contractKeyReserved: свободный ключ достаётся первому и повтором не считается.
func contractKeyReserved(t *testing.T, uow app.UnitOfWork) {
	taskID := todotest.MustTaskID(t)

	stored, replayed := reserve(t, uow, testKey, taskID, testFingerprint)

	if replayed {
		t.Error("первый запрос принят за повтор")
	}
	if stored != taskID {
		t.Errorf("задача = %s, ожидалась %s", stored, taskID)
	}
}

// contractKeyReplayed: повтор не создаёт вторую задачу, а возвращает первую.
func contractKeyReplayed(t *testing.T, uow app.UnitOfWork) {
	first := todotest.MustTaskID(t)
	reserve(t, uow, testKey, first, testFingerprint)

	// Второй запрос принёс тот же ключ и собрался создать другую задачу.
	stored, replayed := reserve(t, uow, testKey, todotest.MustTaskID(t), testFingerprint)

	if !replayed {
		t.Error("повтор не опознан")
	}
	if stored != first {
		t.Errorf("задача = %s, ожидалась %s — повтор создал новую", stored, first)
	}
}

// contractKeyScopedToOwner: одинаковый ключ у разных владельцев — разные ключи.
// Иначе чужой ключ отдавал бы постороннему чужую задачу.
func contractKeyScopedToOwner(t *testing.T, uow app.UnitOfWork) {
	mine := todotest.MustTaskID(t)
	reserve(t, uow, testKey, mine, testFingerprint)

	theirs := todotest.MustTaskID(t)
	key := app.IdempotencyKey{
		OwnerID:     todotest.MustOwnerID(t, "user-13"),
		Key:         testKey,
		Fingerprint: testFingerprint,
		TaskID:      theirs,
	}

	stored, replayed := reserveKey(t, uow, key)

	if replayed {
		t.Error("ключ другого владельца принят за повтор")
	}
	if stored != theirs {
		t.Errorf("задача = %s, ожидалась %s", stored, theirs)
	}
}

// contractKeyReuseRejected: тот же ключ с другим содержимым — не повтор.
// Отдать на него прошлый ответ значило бы ответить не на тот вопрос.
func contractKeyReuseRejected(t *testing.T, uow app.UnitOfWork) {
	reserve(t, uow, testKey, todotest.MustTaskID(t), testFingerprint)

	err := uow.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		_, _, err := tx.Keys().Reserve(ctx, app.IdempotencyKey{
			OwnerID:     todotest.MustOwnerID(t, testOwner),
			Key:         testKey,
			Fingerprint: []byte("другой запрос"),
			TaskID:      todotest.MustTaskID(t),
		})
		return err
	})

	if !errors.Is(err, app.ErrIdempotencyKeyReused) {
		t.Fatalf("ожидалась ErrIdempotencyKeyReused, получено: %v", err)
	}
}

// contractKeyRolledBack: ключ живёт в той же работе, что и задача. Не сложись
// работа — ключ обязан освободиться, иначе клиент, чей запрос отказал, больше
// никогда не смог бы его повторить.
func contractKeyRolledBack(t *testing.T, uow app.UnitOfWork) {
	err := uow.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		if _, _, err := tx.Keys().Reserve(ctx, testKeyFor(t, todotest.MustTaskID(t))); err != nil {
			return err
		}
		return errWorkFailed
	})
	if !errors.Is(err, errWorkFailed) {
		t.Fatalf("ожидалась ошибка работы, получено: %v", err)
	}

	taskID := todotest.MustTaskID(t)
	stored, replayed := reserve(t, uow, testKey, taskID, testFingerprint)

	if replayed {
		t.Error("ключ остался занятым после отката работы")
	}
	if stored != taskID {
		t.Errorf("задача = %s, ожидалась %s", stored, taskID)
	}
}

// contractConcurrentReplays: одновременные повторы одного ключа дают ровно
// одну задачу. Проверка имеет смысл только на настоящем замке: двойник
// разрешает гонку мьютексом, база — уникальным индексом.
func contractConcurrentReplays(t *testing.T, uow app.UnitOfWork) {
	const writers = 8

	var (
		wg      sync.WaitGroup
		fresh   atomic.Int64
		results = make(chan todo.TaskID, writers)
	)

	wg.Add(writers)
	for range writers {
		go func() {
			defer wg.Done()

			taskID := todotest.MustTaskID(t)

			var (
				stored   todo.TaskID
				replayed bool
			)
			err := uow.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
				var err error
				stored, replayed, err = tx.Keys().Reserve(ctx, testKeyFor(t, taskID))
				return err
			})
			if err != nil {
				t.Errorf("Do(...) вернул ошибку: %v", err)
				return
			}

			if !replayed {
				fresh.Add(1)
			}
			results <- stored
		}()
	}
	wg.Wait()
	close(results)

	if fresh.Load() != 1 {
		t.Errorf("ключ занят %d раз, ожидался 1", fresh.Load())
	}

	var first todo.TaskID
	for stored := range results {
		if first.IsZero() {
			first = stored
			continue
		}
		if stored != first {
			t.Fatalf("параллельные повторы вернули разные задачи: %s и %s", first, stored)
		}
	}
}

// Опорные значения ключа. Отпечаток здесь произвольный: его считает сценарий,
// а хранилищу довольно того, что он сравним.
var testFingerprint = []byte("отпечаток запроса")

const testKey = "key-42"

// testKeyFor собирает ключ тестового владельца для указанной задачи.
func testKeyFor(t *testing.T, taskID todo.TaskID) app.IdempotencyKey {
	t.Helper()

	return app.IdempotencyKey{
		OwnerID:     todotest.MustOwnerID(t, testOwner),
		Key:         testKey,
		Fingerprint: testFingerprint,
		TaskID:      taskID,
	}
}

// reserve занимает ключ тестового владельца или валит тест.
func reserve(t *testing.T, uow app.UnitOfWork, key string, taskID todo.TaskID, fingerprint []byte) (todo.TaskID, bool) {
	t.Helper()

	return reserveKey(t, uow, app.IdempotencyKey{
		OwnerID:     todotest.MustOwnerID(t, testOwner),
		Key:         key,
		Fingerprint: fingerprint,
		TaskID:      taskID,
	})
}

// reserveKey занимает ключ в отдельной работе или валит тест.
func reserveKey(t *testing.T, uow app.UnitOfWork, key app.IdempotencyKey) (todo.TaskID, bool) {
	t.Helper()

	var (
		stored   todo.TaskID
		replayed bool
	)
	err := uow.Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		var err error
		stored, replayed, err = tx.Keys().Reserve(ctx, key)
		return err
	})
	if err != nil {
		t.Fatalf("Do(...) вернул ошибку: %v", err)
	}

	return stored, replayed
}
