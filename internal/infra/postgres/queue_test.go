package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
	"github.com/deliseev/todoer/internal/infra/outbox"
	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/infra/postgres/pgtest"
)

// Очередь и слушатель обязаны удовлетворять портам доставщика.
var (
	_ outbox.Queue  = (*postgres.Queue)(nil)
	_ outbox.Waiter = (*postgres.Listener)(nil)
)

// TestQueueTake: то, чего порт очереди не обещает, а таблица обязана делать.
func TestQueueTake(t *testing.T) {
	t.Parallel()

	t.Run("сообщение несёт поля события", func(t *testing.T) {
		t.Parallel()

		// Ради этого маппер и написан руками: encoding/json выдал бы вместо
		// заголовка пустой объект — поля значимых объектов закрыты.
		pool := newPool(t)
		task := todotest.NewTask(t, testOwner, testNow)
		queueTask(t, pool, task)

		messages := takeAll(t, postgres.NewQueue(pool))
		if len(messages) != 1 {
			t.Fatalf("взято %d сообщений, ожидалось 1", len(messages))
		}

		message := messages[0]
		if message.Name != todo.EventTaskCreated {
			t.Errorf("имя события = %q, ожидалось %q", message.Name, todo.EventTaskCreated)
		}
		if message.AggregateID != task.ID().String() {
			t.Errorf("агрегат = %q, ожидался %q", message.AggregateID, task.ID())
		}
		if !message.OccurredAt.Equal(testNow) {
			t.Errorf("момент = %s, ожидался %s", message.OccurredAt, testNow)
		}

		var payload map[string]any
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			t.Fatalf("нагрузка не разбирается: %v", err)
		}
		if payload["title"] != task.Title().String() {
			t.Errorf("заголовок в нагрузке = %v, ожидался %q", payload["title"], task.Title())
		}
		if payload["owner_id"] != testOwner {
			t.Errorf("владелец в нагрузке = %v, ожидался %q", payload["owner_id"], testOwner)
		}
	})

	t.Run("доставленное больше не выдаётся", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queue := postgres.NewQueue(pool)
		queueTask(t, pool, todotest.NewTask(t, testOwner, testNow))

		if taken := takeAll(t, queue); len(taken) != 1 {
			t.Fatalf("взято %d сообщений, ожидалось 1", len(taken))
		}
		if taken := takeAll(t, queue); len(taken) != 0 {
			t.Errorf("доставленное сообщение выдано повторно: %v", taken)
		}
	})

	t.Run("отказ доставки оставляет сообщение в очереди", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queue := postgres.NewQueue(pool)
		queueTask(t, pool, todotest.NewTask(t, testOwner, testNow))

		deliveryErr := errors.New("шина недоступна")
		taken, err := queue.Take(t.Context(), 10,
			func(context.Context, []outbox.Message) error { return deliveryErr })

		if !errors.Is(err, deliveryErr) {
			t.Fatalf("ожидалась ошибка доставки, получено: %v", err)
		}
		if taken != 0 {
			t.Errorf("взято %d сообщений, ожидалось 0 — отказ засчитан за доставку", taken)
		}

		// Сообщение на месте, а причина отказа записана: очередь, которая
		// не пустеет, обязана уметь объяснить почему.
		attempts, lastError := deliveryAttempt(t, pool)
		if attempts != 1 {
			t.Errorf("попыток = %d, ожидалась 1", attempts)
		}
		if lastError != deliveryErr.Error() {
			t.Errorf("причина = %q, ожидалась %q", lastError, deliveryErr)
		}
		if again := takeAll(t, queue); len(again) != 1 {
			t.Errorf("после отказа доставки в очереди %d сообщений, ожидалось 1", len(again))
		}
	})

	t.Run("недоставленное сообщение не держит очередь", func(t *testing.T) {
		t.Parallel()

		// Доставка идёт партиями, и партия уезжает целиком или никак.
		// Значит сообщение, которое шина не принимает, тащило бы за собой
		// всё, что встало за ним: очередь стояла бы вечно, а каждая побудка
		// заново упиралась бы в него же.
		pool := newPool(t)
		queue := postgres.NewQueue(pool)

		stuck := todotest.NewTask(t, testOwner, testNow)
		queueTask(t, pool, stuck)

		deliveryErr := errors.New("шина не принимает это сообщение")
		if _, err := queue.Take(t.Context(), 10,
			func(context.Context, []outbox.Message) error { return deliveryErr },
		); !errors.Is(err, deliveryErr) {
			t.Fatalf("ожидалась ошибка доставки, получено: %v", err)
		}

		fresh := todotest.NewTask(t, testOwner, testNow)
		queueTask(t, pool, fresh)

		taken := takeAll(t, queue)
		if len(taken) != 1 {
			t.Fatalf("взято %d сообщений, ожидалось 1: неудачное утащило за собой свежее", len(taken))
		}
		if taken[0].AggregateID != fresh.ID().String() {
			t.Errorf("доставлено сообщение задачи %s, ожидалась %s",
				taken[0].AggregateID, fresh.ID())
		}

		// И оно не потеряно: когда свежих не осталось, очередь возвращается
		// к нему. Пропускать — не то же самое, что выбрасывать.
		again := takeAll(t, queue)
		if len(again) != 1 {
			t.Fatalf("взято %d сообщений, ожидалось 1: пропущенное сообщение потерялось", len(again))
		}
		if again[0].AggregateID != stuck.ID().String() {
			t.Errorf("доставлено сообщение задачи %s, ожидалась %s",
				again[0].AggregateID, stuck.ID())
		}
	})

	t.Run("причина отказа доставки не теряется, когда её не удалось записать", func(t *testing.T) {
		t.Parallel()

		// Отметка о неудаче и сама неудача — разные вещи, и провалиться
		// может любая. Останься наружу только «не удалось отметить», причина,
		// по которой шина отказала, не попала бы ни в лог, ни в таблицу:
		// откат снимает и запись attempts с last_error.
		pool := newPool(t)
		queueTask(t, pool, todotest.NewTask(t, testOwner, testNow))

		ctx, cancel := context.WithCancel(t.Context())
		deliveryErr := errors.New("шина отказала")

		_, err := postgres.NewQueue(pool).Take(ctx, 10,
			func(context.Context, []outbox.Message) error {
				// Отмена рушит саму отметку: транзакции больше нечем ни
				// писать, ни фиксироваться.
				cancel()
				return deliveryErr
			})

		if err == nil {
			t.Fatal("Take(...) не вернул ошибки")
		}
		if !strings.Contains(err.Error(), deliveryErr.Error()) {
			t.Errorf("ошибка %q не объясняет, почему доставка не удалась", err)
		}
	})

	t.Run("порция ограничена и идёт по порядку", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queue := postgres.NewQueue(pool)
		for range 3 {
			queueTask(t, pool, todotest.NewTask(t, testOwner, testNow))
		}

		var first []outbox.Message
		taken, err := queue.Take(t.Context(), 2, func(_ context.Context, messages []outbox.Message) error {
			first = messages
			return nil
		})
		if err != nil {
			t.Fatalf("Take(...) вернул ошибку: %v", err)
		}
		// Fatalf, а не Errorf: без двух сообщений проверка порядка ниже просто
		// не выполнится, и тест сойдётся сам с собой.
		if taken != 2 {
			t.Fatalf("взято %d сообщений, ожидалось 2", taken)
		}
		if first[0].ID >= first[1].ID {
			t.Errorf("сообщения выданы не по порядку: %d, %d", first[0].ID, first[1].ID)
		}
	})

	t.Run("пустая очередь доставку не дёргает", func(t *testing.T) {
		t.Parallel()

		called := false
		taken, err := postgres.NewQueue(newPool(t)).Take(t.Context(), 10,
			func(context.Context, []outbox.Message) error {
				called = true
				return nil
			})

		if err != nil {
			t.Fatalf("Take(...) вернул ошибку: %v", err)
		}
		if taken != 0 {
			t.Errorf("взято %d сообщений, ожидалось 0", taken)
		}
		if called {
			t.Error("доставка вызвана на пустой очереди")
		}
	})
}

// TestListenerWakesOnNotify: уведомление отправляется в той же транзакции,
// что и событие, и обязано разбудить слушателя.
//
// Каналы уведомлений живут в пределах базы, а pgtest выдаёт каждому тесту
// свою, поэтому параллельные тесты друг друга не будят.
func TestListenerWakesOnNotify(t *testing.T) {
	t.Parallel()

	dsn := pgtest.NewDSN(t)

	pool, err := postgres.Open(t.Context(), dsn)
	if err != nil {
		t.Fatalf("подключение к базе теста: %v", err)
	}
	t.Cleanup(pool.Close)

	listener := postgres.NewListener(dsn)
	t.Cleanup(func() { listener.Close(context.WithoutCancel(t.Context())) })

	// Первое ожидание подключается и подписывается — до него уведомлять
	// некого. Срок короткий: тест проверяет, что побудка доходит, а не что
	// страховочный опрос работает.
	if err := listener.Wait(t.Context(), 100*time.Millisecond); err != nil {
		t.Fatalf("Wait(...) до подписки вернул ошибку: %v", err)
	}

	woke := make(chan error, 1)
	go func() { woke <- listener.Wait(context.WithoutCancel(t.Context()), 10*time.Second) }()

	// Небольшая пауза, чтобы ожидание точно началось: уведомление,
	// отправленное раньше подписки, не повторится.
	time.Sleep(50 * time.Millisecond)
	queueTask(t, pool, todotest.NewTask(t, testOwner, testNow))

	select {
	case err := <-woke:
		if err != nil {
			t.Fatalf("Wait(...) вернул ошибку: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("слушатель не проснулся по уведомлению")
	}
}

// TestListenerReturnsAfterSubscribing: только что созданная подписка не ждёт.
//
// Подписка возникает внутри первого ожидания, то есть уже после того, как
// доставщик выгреб очередь, — и всё, о чём уведомили в этом промежутке, не
// повторится. Вернувшись сразу, слушатель заставляет доставщика сходить в
// очередь ещё раз, теперь уже подписанным. Проверка ловит настоящий дефект:
// без неё событие, созданное сразу после запуска, ждало бы страховочного
// опроса — минуту вместо миллисекунд.
func TestListenerReturnsAfterSubscribing(t *testing.T) {
	t.Parallel()

	listener := postgres.NewListener(pgtest.NewDSN(t))
	t.Cleanup(func() { listener.Close(context.WithoutCancel(t.Context())) })

	started := time.Now()
	if err := listener.Wait(t.Context(), time.Minute); err != nil {
		t.Fatalf("Wait(...) вернул ошибку: %v", err)
	}

	// Срок в минуту: дождись слушатель его — значит подписка создана и он ушёл
	// ждать на ней, пропустив всё, что случилось до неё.
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("первое ожидание заняло %s: подписка создана, а очередь не перечитана", waited)
	}
}

// TestListenerSurvivesTimeout: истёкший срок — не ошибка и не повод терять
// соединение. Страховочный опрос иначе ронял бы подписку каждую минуту.
func TestListenerSurvivesTimeout(t *testing.T) {
	t.Parallel()

	listener := postgres.NewListener(pgtest.NewDSN(t))
	t.Cleanup(func() { listener.Close(context.WithoutCancel(t.Context())) })

	for attempt := range 3 {
		if err := listener.Wait(t.Context(), 50*time.Millisecond); err != nil {
			t.Fatalf("Wait(...) на попытке %d вернул ошибку: %v", attempt+1, err)
		}
	}
}

// queueTask кладёт задачу вместе с её событиями одной работой.
func queueTask(t *testing.T, pool *pgxpool.Pool, task *todo.Task) {
	t.Helper()

	err := postgres.NewUnitOfWork(pool).Do(t.Context(), func(ctx context.Context, tx app.Tx) error {
		if err := tx.Tasks().Save(ctx, task, 0); err != nil {
			return err
		}
		return tx.Outbox().Add(ctx, task.PullEvents())
	})
	if err != nil {
		t.Fatalf("Do(...) вернул ошибку: %v", err)
	}
}

// takeAll забирает всё, что есть в очереди, считая доставку успешной.
func takeAll(t *testing.T, queue *postgres.Queue) []outbox.Message {
	t.Helper()

	var taken []outbox.Message
	if _, err := queue.Take(t.Context(), 100, func(_ context.Context, messages []outbox.Message) error {
		taken = messages
		return nil
	}); err != nil {
		t.Fatalf("Take(...) вернул ошибку: %v", err)
	}

	return taken
}

// deliveryAttempt читает счётчик попыток и последнюю причину отказа.
func deliveryAttempt(t *testing.T, pool *pgxpool.Pool) (int, string) {
	t.Helper()

	var (
		attempts  int
		lastError *string
	)
	err := pool.QueryRow(t.Context(),
		"SELECT attempts, last_error FROM outbox ORDER BY id LIMIT 1").Scan(&attempts, &lastError)
	if err != nil {
		t.Fatalf("чтение попыток: %v", err)
	}

	if lastError == nil {
		return attempts, ""
	}
	return attempts, *lastError
}
