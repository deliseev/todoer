package app_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

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

	switch {
	case ok && stored.Version != loadedVersion:
		return fmt.Errorf("fake: save task %s (loaded version %d, stored version %d): %w",
			snapshot.ID, loadedVersion, stored.Version, app.ErrVersionConflict)
	case !ok && loadedVersion != 0:
		return fmt.Errorf("fake: save task %s (loaded version %d, not stored anymore): %w",
			snapshot.ID, loadedVersion, app.ErrVersionConflict)
	case ok && loadedVersion == 0:
		return fmt.Errorf("fake: insert task %s (already stored at version %d): %w",
			snapshot.ID, stored.Version, app.ErrVersionConflict)
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

// put кладёт снимок в обход проверки версий — так тест изображает запись,
// сделанную кем-то другим между Get и Save сценария.
func (r *fakeRepository) put(snapshot todo.TaskSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tasks[snapshot.ID.String()] = snapshot
}

// saveCount возвращает число состоявшихся записей.
func (r *fakeRepository) saveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.saves
}

// recordingPublisher запоминает всё, что сценарии отдали на публикацию.
type recordingPublisher struct {
	mu     sync.Mutex
	events []todo.DomainEvent
	calls  int
	err    error
	// sawEmpty отмечает публикацию пустой партии: успешная мутация всегда
	// порождает событие, поэтому дёргать публикатор впустую сценарию незачем.
	sawEmpty bool
	// ctxErr — состояние контекста на момент вызова. Сам контекст в поле
	// не кладём: он живёт ровно столько, сколько вызов.
	ctxErr error
}

// Publish запоминает партию событий.
func (p *recordingPublisher) Publish(ctx context.Context, events []todo.DomainEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	p.ctxErr = ctx.Err()
	if len(events) == 0 {
		p.sawEmpty = true
	}
	p.events = append(p.events, events...)

	return p.err
}

// failWith заставляет публикатор отказывать.
func (p *recordingPublisher) failWith(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.err = err
}

// published возвращает имена опубликованных событий в порядке публикации.
func (p *recordingPublisher) published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return eventNames(p.events)
}

// eventAt возвращает опубликованное событие по его порядковому номеру.
func (p *recordingPublisher) eventAt(i int) todo.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.events[i]
}

// callCount возвращает число обращений к публикатору.
func (p *recordingPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.calls
}

// contextErr возвращает состояние контекста на момент последнего вызова.
func (p *recordingPublisher) contextErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.ctxErr
}

// sawEmptyBatch сообщает, что публикатор хотя бы раз получил пустую партию.
func (p *recordingPublisher) sawEmptyBatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.sawEmpty
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
