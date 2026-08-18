package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// keyedCommand — команда создания с ключом идемпотентности.
func keyedCommand(key string) app.CreateTaskCommand {
	return app.CreateTaskCommand{
		OwnerID:        testOwner,
		Title:          "Купить молоко",
		Description:    "Два литра, в магазине у дома",
		Priority:       "high",
		DueDate:        &testDue,
		IdempotencyKey: key,
	}
}

func TestCreateTaskIsIdempotent(t *testing.T) {
	t.Run("повтор возвращает ту же задачу и не создаёт вторую", func(t *testing.T) {
		// Ради этого пункт и затевался: повтор — таймаут, ретрай балансировщика,
		// упавший клиент — до сих пор создавал задачу-дубль.
		env := newTestEnv(t)

		first, err := env.service.CreateTask(t.Context(), keyedCommand("key-42"))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		second, err := env.service.CreateTask(t.Context(), keyedCommand("key-42"))
		if err != nil {
			t.Fatalf("повторный CreateTask(...) вернул ошибку: %v", err)
		}

		if second != first {
			t.Errorf("повтор вернул задачу %s, ожидалась %s", second, first)
		}
		if saves := env.repo.saveCount(); saves != 1 {
			t.Errorf("записей в хранилище %d, ожидалась 1 — повтор создал дубль", saves)
		}
	})

	t.Run("повтор не порождает второго события", func(t *testing.T) {
		// Событие описывает то, что произошло. Повтор не произошёл — значит
		// и рассказывать о нём нечего, иначе подписчик увидит два создания
		// одной задачи.
		env := newTestEnv(t)

		if _, err := env.service.CreateTask(t.Context(), keyedCommand("key-42")); err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}
		if _, err := env.service.CreateTask(t.Context(), keyedCommand("key-42")); err != nil {
			t.Fatalf("повторный CreateTask(...) вернул ошибку: %v", err)
		}

		want := []string{todo.EventTaskCreated}
		if got := env.outbox.queued(); len(got) != 1 || got[0] != want[0] {
			t.Errorf("в очереди %v, ожидалось %v", got, want)
		}
	})

	t.Run("разные ключи — разные задачи", func(t *testing.T) {
		env := newTestEnv(t)

		first, err := env.service.CreateTask(t.Context(), keyedCommand("key-1"))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}
		second, err := env.service.CreateTask(t.Context(), keyedCommand("key-2"))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		if second == first {
			t.Error("разные ключи вернули одну задачу")
		}
	})

	t.Run("без ключа повтор создаёт новую задачу", func(t *testing.T) {
		// Ключ необязателен, и поведение без него прежнее: клиент, который
		// про идемпотентность не просил, получает ровно то, что просил.
		env := newTestEnv(t)

		first, err := env.service.CreateTask(t.Context(), keyedCommand(""))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}
		second, err := env.service.CreateTask(t.Context(), keyedCommand(""))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		if second == first {
			t.Error("без ключа повтор вернул ту же задачу")
		}
		if saves := env.repo.saveCount(); saves != 2 {
			t.Errorf("записей в хранилище %d, ожидалось 2", saves)
		}
	})

	t.Run("тот же ключ с другим запросом отвергается", func(t *testing.T) {
		env := newTestEnv(t)

		if _, err := env.service.CreateTask(t.Context(), keyedCommand("key-42")); err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		other := keyedCommand("key-42")
		other.Title = "Купить кефир"

		_, err := env.service.CreateTask(t.Context(), other)

		if !errors.Is(err, app.ErrIdempotencyKeyReused) {
			t.Fatalf("ожидалась ErrIdempotencyKeyReused, получено: %v", err)
		}
		if saves := env.repo.saveCount(); saves != 1 {
			t.Errorf("записей в хранилище %d, ожидалась 1", saves)
		}
	})

	t.Run("запросы, одинаковые для домена, считаются повтором", func(t *testing.T) {
		// Отпечаток берётся с разобранных значений, а не с тела запроса:
		// лишние пробелы домен уже срезал, и считать такой запрос другим
		// значило бы отвергать честный повтор.
		env := newTestEnv(t)

		first, err := env.service.CreateTask(t.Context(), keyedCommand("key-42"))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}

		padded := keyedCommand("key-42")
		padded.Title = "  Купить молоко  "

		second, err := env.service.CreateTask(t.Context(), padded)
		if err != nil {
			t.Fatalf("повторный CreateTask(...) вернул ошибку: %v", err)
		}
		if second != first {
			t.Errorf("повтор вернул задачу %s, ожидалась %s", second, first)
		}
	})

	t.Run("отказ хранилища ключей откатывает создание", func(t *testing.T) {
		env := newTestEnv(t)
		keysErr := errors.New("хранилище ключей недоступно")
		env.keys.failReserve(keysErr)

		id, err := env.service.CreateTask(t.Context(), keyedCommand("key-42"))

		if !errors.Is(err, keysErr) {
			t.Fatalf("ожидалась ошибка хранилища ключей, получено: %v", err)
		}
		if _, ok := env.repo.stored(id); ok {
			t.Error("задача сохранена, хотя ключ записать не удалось")
		}
		if len(env.outbox.queued()) != 0 {
			t.Errorf("в очереди %d событий, ожидалось 0", len(env.outbox.queued()))
		}
	})

	t.Run("негодная команда до ключа не доходит", func(t *testing.T) {
		// Разбор идёт до работы: негодная команда не должна стоить ни
		// транзакции, ни занятого ключа.
		env := newTestEnv(t)

		bad := keyedCommand("key-42")
		bad.Title = "   "

		if _, err := env.service.CreateTask(t.Context(), bad); !errors.Is(err, todo.ErrEmptyTitle) {
			t.Fatalf("ожидалась ErrEmptyTitle, получено: %v", err)
		}

		// Ключ свободен: следующий запрос с ним создаёт задачу, а не получает
		// повтор.
		id, err := env.service.CreateTask(t.Context(), keyedCommand("key-42"))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}
		if _, ok := env.repo.stored(id); !ok {
			t.Error("задача не сохранена: ключ остался занятым после негодной команды")
		}
	})

	t.Run("непомерный ключ отвергается до работы", func(t *testing.T) {
		// Ключ приходит от клиента и ложится в хранилище под замок, а замок
		// не резиновый. Без потолка непомерный ключ доходил бы до записи и
		// возвращался отказом хранилища — то есть 500 за то, что испортил
		// клиент, да ещё и в счётчике 5xx.
		env := newTestEnv(t)

		long := keyedCommand(strings.Repeat("k", app.MaxIdempotencyKeyLength+1))

		_, err := env.service.CreateTask(t.Context(), long)

		if !errors.Is(err, app.ErrIdempotencyKeyTooLong) {
			t.Fatalf("ожидалась ErrIdempotencyKeyTooLong, получено: %v", err)
		}
		if saves := env.repo.saveCount(); saves != 0 {
			t.Errorf("записей в хранилище %d, ожидалось 0: негодный ключ дошёл до работы", saves)
		}
	})

	t.Run("ключ ровно в потолок проходит", func(t *testing.T) {
		// Граница с обеих сторон: потолок обязан быть достижимым, иначе
		// проверка отвергает законный ключ на единицу короче непомерного.
		env := newTestEnv(t)

		id, err := env.service.CreateTask(t.Context(),
			keyedCommand(strings.Repeat("k", app.MaxIdempotencyKeyLength)))
		if err != nil {
			t.Fatalf("CreateTask(...) вернул ошибку: %v", err)
		}
		if _, ok := env.repo.stored(id); !ok {
			t.Error("задача не сохранена")
		}
	})
}
