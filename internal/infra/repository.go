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
		return nil, 0, fmt.Errorf("infra: get task %s: %w", taskID, app.ErrTaskNotFound)
	}

	// Восстановление идёт уже без блокировки: снимок скопирован, а работа
	// домена хранилища не касается.
	//
	// Отказ восстановления здесь недостижим и покрытием не берётся: в map
	// попадают только снимки живых агрегатов, а их домен уже проверил. Ветка
	// оживёт вместе с персистентным хранилищем, где снимок собирается из строк
	// и может не сойтись с инвариантами. Убирать её до тех пор нельзя: Get
	// обещает вызывающему валидный агрегат, и обещание должно опираться
	// на проверку, а не на устройство конкретной реализации.
	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		return nil, 0, fmt.Errorf("infra: reconstitute task %s: %w", taskID, err)
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

	// Порядок веток важен. Версия живой задачи всегда не меньше единицы,
	// поэтому loadedVersion == 0 однозначно читается как «не поднимал», а не
	// как «поднял нулевую версию». Стой эта ветка последней, её перехватывала
	// бы общая проверка версий, и вставка на занятое место объяснялась бы
	// расхождением номеров вместо того, что случилось на самом деле.
	switch {
	case ok && loadedVersion == 0:
		// Писатель считает задачу новой, а место уже занято.
		return fmt.Errorf("infra: insert task %s (already stored at version %d): %w",
			task.ID(), stored.Version, app.ErrVersionConflict)
	case !ok && loadedVersion != 0:
		// Задачу поднимали из хранилища, а сейчас её там нет.
		return fmt.Errorf("infra: save task %s (loaded version %d, not stored anymore): %w",
			task.ID(), loadedVersion, app.ErrVersionConflict)
	case ok && stored.Version != loadedVersion:
		return fmt.Errorf("infra: save task %s (loaded version %d, stored version %d): %w",
			task.ID(), loadedVersion, stored.Version, app.ErrVersionConflict)
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
