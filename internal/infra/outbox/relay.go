package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Сроки и размеры доставщика.
const (
	// batchSize — сколько сообщений берётся за раз. Порция нужна, чтобы
	// накопившаяся очередь не поднималась в память целиком.
	batchSize = 100

	// pollInterval — как часто очередь проверяется без побудки. Редко и
	// намеренно: это страховка от пропущенного уведомления, а не основной
	// способ узнать о новом событии. Пропущенное уведомление стоит задержки,
	// а не потери.
	pollInterval = time.Minute

	// retryDelay — пауза после отказа ожидания, чтобы не крутиться вплотную
	// вокруг недоступной базы.
	retryDelay = 5 * time.Second
)

// Relay разгребает исходящую очередь: забирает сообщения и отдаёт их
// публикатору, пока очередь не опустеет, а затем ждёт побудки.
type Relay struct {
	queue     Queue
	publisher Publisher
	waiter    Waiter
}

// NewRelay собирает доставщика. Все зависимости обязательны.
func NewRelay(queue Queue, publisher Publisher, waiter Waiter) (*Relay, error) {
	if queue == nil {
		return nil, fmt.Errorf("outbox: build relay (queue): %w", errMissingDependency)
	}
	if publisher == nil {
		return nil, fmt.Errorf("outbox: build relay (publisher): %w", errMissingDependency)
	}
	if waiter == nil {
		return nil, fmt.Errorf("outbox: build relay (waiter): %w", errMissingDependency)
	}

	return &Relay{queue: queue, publisher: publisher, waiter: waiter}, nil
}

// Run разгребает очередь, пока не отменят ctx.
//
// Порядок здесь важнее, чем кажется: выгребание идёт до ожидания, а не после.
// Уведомления, случившиеся, пока доставщика не было, никто не повторит —
// значит первый заход обязан быть безусловным, и после каждого разрыва связи
// тоже. Ожидание — только способ не крутиться вхолостую между событиями.
//
// Отмена контекста — штатное завершение, поэтому Run ничего не возвращает:
// отказы доставки уезжают в лог, а сообщения остаются в очереди и приедут
// на следующем заходе.
func (r *Relay) Run(ctx context.Context) {
	for {
		r.drain(ctx)

		if ctx.Err() != nil {
			return
		}

		if err := r.waiter.Wait(ctx, pollInterval); err != nil {
			if ctx.Err() != nil {
				return
			}

			slog.Error("outbox: wait for events", "error", err)

			// Пауза после отказа: без неё цикл крутился бы вокруг недоступной
			// базы вплотную, забивая лог и соединения.
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
		}
	}
}

// drain выгребает очередь до дна.
//
// Неполная порция означает, что очередь исчерпана: брать больше нечего, и
// следующий заход только зря сходил бы в базу.
func (r *Relay) drain(ctx context.Context) {
	for {
		taken, err := r.queue.Take(ctx, batchSize, r.publisher.Publish)
		if err != nil {
			// Отмена — не беда, а штатная остановка: о ней сообщит Run.
			if ctx.Err() == nil {
				slog.Error("outbox: deliver events", "error", err)
			}
			return
		}

		if taken < batchSize {
			return
		}
	}
}
