package infra

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
)

// errNilTask — вместо задачи передали nil.
//
// Хранилище стоит на границе, и мусор с той стороны обязано пережить:
// забытая наверху проверка ошибки не повод ронять процесс целиком.
var errNilTask = errors.New("infra: task is nil")

// InMemoryTaskRepository — реализация app.Repository поверх памяти.
//
// Хранит снимки, а не указатели на агрегат: иначе вызывающий держал бы тот же
// объект, что и хранилище, и менял бы задачу в обход Save, версии и событий.
//
// Оптимистичная блокировка сверяется с loadedVersion — тем, что вернул Get.
// Число мутаций между Get и Save при этом не ограничено: накопить их и
// записать разом законно, лишь бы за это время никто другой не записал своё.
// Версия подъёма живёт здесь, на границе, а не в агрегате: «откуда взялся
// этот объект в памяти» — вопрос хранилища, домену его задавать незачем.
type InMemoryTaskRepository struct {
	// RWMutex, а не Mutex: чтений у хранилища заметно больше, чем записей,
	// и держать их в одной очереди незачем.
	mu        sync.RWMutex
	snapshots map[todo.TaskID]todo.TaskSnapshot
}

// NewInMemoryTaskRepository создаёт пустое хранилище.
func NewInMemoryTaskRepository() *InMemoryTaskRepository {
	return &InMemoryTaskRepository{
		snapshots: make(map[todo.TaskID]todo.TaskSnapshot),
	}
}

// Get поднимает задачу из памяти и сообщает версию, которой она пришла.
func (r *InMemoryTaskRepository) Get(ctx context.Context, taskID todo.TaskID) (*todo.Task, int, error) {
	// Контекст проверяется до блокировки: отменённому запросу незачем ждать
	// очереди, чтобы узнать, что он никому не нужен.
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	snapshot, ok := r.snapshot(taskID)
	if !ok {
		return nil, 0, fmt.Errorf("%w: задача %s", app.ErrTaskNotFound, taskID)
	}

	// Восстановление идёт уже без блокировки: снимок скопирован, а работа
	// домена хранилища не касается.
	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		return nil, 0, fmt.Errorf("infra: восстановить задачу %s: %w", taskID, err)
	}

	return task, snapshot.Version, nil
}

// Save сохраняет задачу, соблюдая оптимистичную блокировку по версии.
func (r *InMemoryTaskRepository) Save(ctx context.Context, task *todo.Task, loadedVersion int) error {
	// Аргумент проверяется раньше контекста: нарушение контракта остаётся
	// нарушением независимо от того, ждёт ли ещё кто-то ответа.
	if task == nil {
		return errNilTask
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Сверяться надо с тем состоянием, которое этот писатель читал, а не
	// с тем, до которого он домутировал: между Get и Save мутаций может быть
	// сколько угодно, и «на единицу больше» их не переживает.
	stored, ok := r.snapshots[task.ID()]

	switch {
	case ok && stored.Version != loadedVersion:
		return fmt.Errorf("%w: задачу подняли версией %d, сохранена версия %d",
			app.ErrVersionConflict, loadedVersion, stored.Version)
	case !ok && loadedVersion != 0:
		// Задачу поднимали из хранилища, а сейчас её там нет.
		return fmt.Errorf("%w: задача %s поднята версией %d, но в хранилище её нет",
			app.ErrVersionConflict, task.ID(), loadedVersion)
	case ok && loadedVersion == 0:
		// Писатель считает задачу новой, а место уже занято.
		return fmt.Errorf("%w: задача %s уже сохранена версией %d",
			app.ErrVersionConflict, task.ID(), stored.Version)
	}

	r.snapshots[task.ID()] = task.Snapshot()

	return nil
}

// snapshot отдаёт копию сохранённого снимка под блокировкой чтения.
func (r *InMemoryTaskRepository) snapshot(taskID todo.TaskID) (todo.TaskSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot, ok := r.snapshots[taskID]
	return snapshot, ok
}
