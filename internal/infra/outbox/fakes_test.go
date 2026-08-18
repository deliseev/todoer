package outbox_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/infra/outbox"
)

// fakeQueue — очередь в памяти.
//
// Атомарность изъятия и отметки изображается так же, как её держит база:
// сообщения уходят из очереди только вместе с успешной доставкой, а отказ
// возвращает их на место, оставив след в счётчике попыток.
type fakeQueue struct {
	mu      sync.Mutex
	pending []outbox.Message
	nextID  int64
	tries   int
}

// newFakeQueue создаёт очередь с count сообщениями.
func newFakeQueue(count int) *fakeQueue {
	q := &fakeQueue{nextID: 1}
	q.append(count)
	return q
}

// append добавляет в очередь ещё count сообщений.
func (q *fakeQueue) append(count int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for range count {
		q.pending = append(q.pending, outbox.Message{
			ID:          q.nextID,
			AggregateID: "0123456789abcdef0123456789abcdef",
			Name:        "todo.task.created",
			Payload:     []byte(`{}`),
			OccurredAt:  time.Now(),
		})
		q.nextID++
	}
}

// Take забирает порцию сообщений и отдаёт их доставке.
func (q *fakeQueue) Take(
	ctx context.Context,
	limit int,
	deliver func(ctx context.Context, messages []outbox.Message) error,
) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Пустую очередь доставщик обязан узнавать по нулю, а публикатора при
	// этом дёргать незачем.
	if len(q.pending) == 0 {
		return 0, nil
	}

	batch := min(limit, len(q.pending))
	messages := q.pending[:batch]

	if err := deliver(ctx, messages); err != nil {
		// Сообщения остаются на месте: недоставленное не теряется.
		q.tries++
		return 0, err
	}

	q.pending = q.pending[batch:]

	return batch, nil
}

// pendingCount возвращает число сообщений, оставшихся в очереди.
func (q *fakeQueue) pendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.pending)
}

// attempts возвращает число учтённых неудачных попыток.
func (q *fakeQueue) attempts() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.tries
}

// fakePublisher запоминает всё, что доставщик отдал наружу.
type fakePublisher struct {
	mu        sync.Mutex
	delivered []outbox.Message
	callCount int
	err       error
}

// Publish запоминает партию сообщений.
func (p *fakePublisher) Publish(_ context.Context, messages []outbox.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.err != nil {
		return p.err
	}
	p.delivered = append(p.delivered, messages...)

	return nil
}

// count возвращает число доставленных сообщений.
func (p *fakePublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.delivered)
}

// calls возвращает число обращений к публикатору.
func (p *fakePublisher) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.callCount
}

// ids возвращает номера доставленных сообщений в порядке доставки.
func (p *fakePublisher) ids() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	ids := make([]int64, len(p.delivered))
	for i, message := range p.delivered {
		ids[i] = message.ID
	}
	return ids
}

// fakeWaiter изображает побудку: тест сам решает, когда доставщик проснётся.
type fakeWaiter struct {
	signals chan struct{}
	waits   chan struct{}
}

func newFakeWaiter() *fakeWaiter {
	return &fakeWaiter{
		signals: make(chan struct{}, 1),
		// Буфер, чтобы доставщик не блокировался, когда теста рядом нет:
		// ожидание — его обычное состояние, а не событие для теста.
		waits: make(chan struct{}, 64),
	}
}

// Wait ждёт побудки, отмены или срока.
//
// Срок здесь настоящий: страховочный опрос в тестах не нужен, но и мешать
// он не должен — тест дожидается своего задолго до минуты.
func (w *fakeWaiter) Wait(ctx context.Context, timeout time.Duration) error {
	select {
	case w.waits <- struct{}{}:
	default:
	}

	select {
	case <-w.signals:
		return nil
	case <-time.After(timeout):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notify будит доставщика.
func (w *fakeWaiter) notify() {
	select {
	case w.signals <- struct{}{}:
	default:
	}
}

// awaitWait дожидается, пока доставщик уснёт: к этому моменту он уже выгреб
// очередь до дна.
func (w *fakeWaiter) awaitWait(t *testing.T) {
	t.Helper()

	select {
	case <-w.waits:
	case <-time.After(waitTimeout):
		t.Fatal("доставщик так и не дошёл до ожидания")
	}
}
