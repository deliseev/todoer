package app_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

// errNilTask — вместо задачи передали nil.
//
// Двойник обязан пережить это ошибкой, как и настоящее хранилище: паника
// вместо отказа — расхождение с портом, а не мелочь оформления.
var errNilTask = errors.New("fake: task is nil")

// fakeRepository — хранилище в памяти для тестов сценариев.
//
// Хранит именно снимки, а не указатели на агрегат: иначе сценарий, забывший
// вызвать Save, всё равно «сохранял» бы изменения через общий объект,
// и тест этого не заметил бы.
type fakeRepository struct {
	mu    sync.Mutex
	tasks map[string]todo.TaskSnapshot

	// getErr и saveErr подменяют результат порта, чтобы проверить поведение
	// сценария при отказе хранилища.
	getErr  error
	saveErr error

	// beforeSave вызывается перед записью и без удерживаемого мьютекса:
	// так тест изображает чужую запись, вклинившуюся между Get и Save.
	beforeSave func()

	gets  int
	saves int
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tasks: make(map[string]todo.TaskSnapshot)}
}

// failGet заставляет хранилище отказывать на чтении.
func (r *fakeRepository) failGet(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.getErr = err
}

// failSave заставляет хранилище отказывать на записи.
func (r *fakeRepository) failSave(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.saveErr = err
}

// onBeforeSave вешает однократный хук, срабатывающий перед записью.
func (r *fakeRepository) onBeforeSave(hook func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.beforeSave = hook
}

// takeBeforeSave забирает хук, оставляя место пустым: он однократный.
func (r *fakeRepository) takeBeforeSave() func() {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook := r.beforeSave
	r.beforeSave = nil
	return hook
}

// Get поднимает задачу из памяти.
func (r *fakeRepository) Get(ctx context.Context, id todo.TaskID) (*todo.Task, int, error) {
	// Настоящее хранилище отменённый запрос не обслуживает, и фейк, который
	// обслуживал бы, разрешал бы сценарию работать после отмены незаметно.
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Считается всякое обращение, включая отказное: тесты проверяют, что
	// негодная команда не стоила похода в хранилище, а не что поход удался.
	r.gets++

	if r.getErr != nil {
		return nil, 0, r.getErr
	}

	snapshot, ok := r.tasks[id.String()]
	if !ok {
		return nil, 0, fmt.Errorf("fake: get task %s: %w", id, app.ErrTaskNotFound)
	}

	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		return nil, 0, err
	}
	return task, snapshot.Version, nil
}

// Save записывает задачу, соблюдая оптимистичную блокировку по версии.
func (r *fakeRepository) Save(ctx context.Context, task *todo.Task, loadedVersion int) error {
	// Аргумент проверяется раньше контекста: нарушение контракта остаётся
	// нарушением независимо от того, ждёт ли ещё кто-то ответа.
	if task == nil {
		return errNilTask
	}
	// Контекст проверяется до хука: хук изображает отмену, случившуюся уже
	// после того, как хранилище приняло запись.
	if err := ctx.Err(); err != nil {
		return err
	}

	if hook := r.takeBeforeSave(); hook != nil {
		hook()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.saveErr != nil {
		return r.saveErr
	}

	// То же правило, что у настоящего хранилища: сверяемся с версией, которой
	// задачу подняли. Копировать сюда упрощённое правило нельзя — двойник
	// перестанет ловить то, на чём споткнётся оригинал.
	snapshot := task.Snapshot()
	stored, ok := r.tasks[snapshot.ID.String()]

	// Порядок веток тот же, что у настоящего хранилища, и по той же причине:
	// loadedVersion == 0 значит «не поднимал», и перехватывать этот случай
	// общей проверкой версий нельзя.
	switch {
	case ok && loadedVersion == 0:
		return fmt.Errorf("fake: insert task %s (already stored at version %d): %w",
			snapshot.ID, stored.Version, app.ErrVersionConflict)
	case !ok && loadedVersion != 0:
		return fmt.Errorf("fake: save task %s (loaded version %d, not stored anymore): %w",
			snapshot.ID, loadedVersion, app.ErrVersionConflict)
	case ok && stored.Version != loadedVersion:
		return fmt.Errorf("fake: save task %s (loaded version %d, stored version %d): %w",
			snapshot.ID, loadedVersion, stored.Version, app.ErrVersionConflict)
	}

	r.tasks[snapshot.ID.String()] = snapshot
	r.saves++

	return nil
}

// stored возвращает сохранённый снимок задачи.
func (r *fakeRepository) stored(id todo.TaskID) (todo.TaskSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot, ok := r.tasks[id.String()]
	return snapshot, ok
}

// copyTasks снимает копию хранилища для возможного отката.
func (r *fakeRepository) copyTasks() map[string]todo.TaskSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	return maps.Clone(r.tasks)
}

// restoreTasks возвращает хранилище к снятому снимку.
func (r *fakeRepository) restoreTasks(tasks map[string]todo.TaskSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tasks = tasks
}

// put кладёт снимок в обход проверки версий — так тест изображает запись,
// сделанную кем-то другим между Get и Save сценария.
func (r *fakeRepository) put(snapshot todo.TaskSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tasks[snapshot.ID.String()] = snapshot
}

// getCount возвращает число обращений на чтение.
func (r *fakeRepository) getCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.gets
}

// saveCount возвращает число состоявшихся записей.
func (r *fakeRepository) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.saves
}

// fakeUnitOfWork — единица работы в памяти.
//
// Атомарность изображается снимком состояния до работы: отказ или паника
// возвращают хранилище и очередь к нему. Настоящая транзакция делает то же
// самое, только руками базы, и подменять здесь правило упрощённым нельзя —
// двойник перестал бы ловить то, на чём споткнётся оригинал.
type fakeUnitOfWork struct {
	// mu держится всю работу: единица работы на то и единица, чтобы чужая
	// запись не вклинилась в середину.
	mu     sync.Mutex
	repo   *fakeRepository
	outbox *fakeOutbox
	keys   *fakeKeys
}

func newFakeUnitOfWork(repo *fakeRepository, outbox *fakeOutbox) *fakeUnitOfWork {
	return &fakeUnitOfWork{repo: repo, outbox: outbox, keys: newFakeKeys()}
}

// Do выполняет работу и фиксирует её результат.
func (u *fakeUnitOfWork) Do(ctx context.Context, work func(ctx context.Context, tx app.Tx) error) error {
	// Отменённому запросу незачем начинать работу, чтобы узнать, что он
	// никому не нужен.
	if err := ctx.Err(); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	tasks, events, keys := u.repo.copyTasks(), u.outbox.copyEvents(), u.keys.copyKeys()

	// Откат снимается только после успеха, поэтому defer возвращает состояние
	// и когда работа вернула ошибку, и когда она вышла паникой.
	committed := false
	defer func() {
		if !committed {
			u.repo.restoreTasks(tasks)
			u.outbox.restoreEvents(events)
			u.keys.restoreKeys(keys)
		}
	}()

	if err := work(ctx, fakeTx{repo: u.repo, outbox: u.outbox, keys: u.keys}); err != nil {
		return err
	}

	committed = true
	return nil
}

// fakeTx — доступ к хранилищам внутри работы.
type fakeTx struct {
	repo   *fakeRepository
	outbox *fakeOutbox
	keys   *fakeKeys
}

// Tasks отдаёт хранилище задач.
func (tx fakeTx) Tasks() app.Repository { return tx.repo }

// Outbox отдаёт исходящую очередь событий.
func (tx fakeTx) Outbox() app.Outbox { return tx.outbox }

// Keys отдаёт хранилище ключей идемпотентности.
func (tx fakeTx) Keys() app.IdempotencyStore { return tx.keys }

// fakeKeys — хранилище ключей идемпотентности в памяти.
//
// Замок здесь мьютекс единицы работы, у настоящего хранилища — уникальный
// индекс, но правило одно: закрепить ключ вправе ровно один запрос.
type fakeKeys struct {
	mu   sync.Mutex
	keys map[string]app.IdempotencyKey

	// reserveErr подменяет результат порта, чтобы проверить поведение
	// сценария при отказе хранилища ключей.
	reserveErr error
}

func newFakeKeys() *fakeKeys {
	return &fakeKeys{keys: make(map[string]app.IdempotencyKey)}
}

// Reserve закрепляет ключ за задачей или сообщает о повторе.
func (k *fakeKeys) Reserve(ctx context.Context, key app.IdempotencyKey) (todo.TaskID, bool, error) {
	if err := ctx.Err(); err != nil {
		return todo.TaskID{}, false, err
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	if k.reserveErr != nil {
		return todo.TaskID{}, false, k.reserveErr
	}

	slot := key.OwnerID.String() + "\x00" + key.Key

	stored, taken := k.keys[slot]
	if !taken {
		k.keys[slot] = key
		return key.TaskID, false, nil
	}

	// Тот же ключ с другим содержимым — не повтор: клиент спросил о другом.
	if !bytes.Equal(stored.Fingerprint, key.Fingerprint) {
		return todo.TaskID{}, false, fmt.Errorf("fake: reserve key %s (task %s): %w",
			key.Key, stored.TaskID, app.ErrIdempotencyKeyReused)
	}

	return stored.TaskID, true, nil
}

// failReserve заставляет хранилище ключей отказывать.
func (k *fakeKeys) failReserve(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.reserveErr = err
}

// copyKeys снимает копию хранилища для возможного отката.
func (k *fakeKeys) copyKeys() map[string]app.IdempotencyKey {
	k.mu.Lock()
	defer k.mu.Unlock()

	return maps.Clone(k.keys)
}

// restoreKeys возвращает хранилище к снятому снимку.
func (k *fakeKeys) restoreKeys(keys map[string]app.IdempotencyKey) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.keys = keys
}

// fakeOutbox — исходящая очередь в памяти.
type fakeOutbox struct {
	mu     sync.Mutex
	events []todo.DomainEvent

	// addErr подменяет результат порта, чтобы проверить поведение сценария
	// при отказе очереди.
	addErr error

	// sawEmpty отмечает запись пустой партии: состоявшаяся мутация всегда
	// порождает событие, поэтому дёргать очередь впустую сценарию незачем.
	sawEmpty bool

	// adds считает обращения к очереди: команда, изменившая несколько полей,
	// обязана положить события одной порцией, а не бегать сюда за каждым.
	adds int
}

func newFakeOutbox() *fakeOutbox {
	return &fakeOutbox{}
}

// Add кладёт события в очередь.
func (o *fakeOutbox) Add(ctx context.Context, events []todo.DomainEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.adds++

	if o.addErr != nil {
		return o.addErr
	}
	// Пустая партия — корректный вход: очередь не место, где решают, что
	// считать изменением. Но сценарию сюда с ней ходить незачем, и тест
	// это замечает.
	if len(events) == 0 {
		o.sawEmpty = true
	}
	o.events = append(o.events, events...)

	return nil
}

// failAdd заставляет очередь отказывать на записи.
func (o *fakeOutbox) failAdd(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.addErr = err
}

// queued возвращает имена событий в очереди в порядке записи.
func (o *fakeOutbox) queued() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return todotest.EventNames(o.events)
}

// addCount возвращает число обращений к очереди.
func (o *fakeOutbox) addCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.adds
}

// sawEmptyBatch сообщает, что очередь хотя бы раз получила пустую партию.
func (o *fakeOutbox) sawEmptyBatch() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.sawEmpty
}

// eventAt возвращает событие очереди по его порядковому номеру.
func (o *fakeOutbox) eventAt(i int) todo.DomainEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.events[i]
}

// copyEvents снимает копию очереди для возможного отката.
func (o *fakeOutbox) copyEvents() []todo.DomainEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.events)
}

// restoreEvents возвращает очередь к снятому снимку.
func (o *fakeOutbox) restoreEvents(events []todo.DomainEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.events = events
}

// stubClock — управляемые часы. Тесты двигают их вручную, поэтому «сейчас»
// в сценариях остаётся таким же предсказуемым, как в тестах домена.
type stubClock struct {
	mu sync.Mutex
	at time.Time
}

// Now возвращает выставленный момент времени.
func (c *stubClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

// set переводит часы на заданный момент.
func (c *stubClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = at
}
