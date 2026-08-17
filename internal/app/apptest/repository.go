// Package apptest содержит наборы тестов контракта для портов слоя сценариев.
//
// Реализаций у app.Repository две — боевое хранилище на Postgres и двойник в
// тестах сценариев, — и расходятся они молча: двойник однажды отвечал паникой
// там, где порт требует ошибку. Устройство у них разное и обязано остаться
// разным: двойнику нужны инъекция отказов и хук перед записью, боевому
// хранилищу они противопоказаны. Совпадать обязано поведение, и оно описано
// здесь один раз, чтобы расхождение ловил go test, а не ревью. Цена изменения
// порта от этого не растёт, а падает: один набор плюс реализации вместо
// набора на каждую, расходящихся поодиночке.
//
// Пакет обычный, а не тестовый: его импортируют тесты двух разных пакетов,
// а из файлов с суффиксом _test импортировать нечего. Так же устроены
// fstest.TestFS и httptest в стандартной библиотеке.
package apptest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

// Опорные моменты времени. Хранилище о времени не думает — оно принимает то,
// что уже проставил домен, — поэтому здесь достаточно констант.
var (
	testNow   = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	testLater = testNow.Add(time.Hour)
	testDue   = testNow.Add(24 * time.Hour)
)

// testOwner — владелец задач в наборе. Хранилище владельцев не различает:
// авторизация живёт в сценарии, а не на этой границе.
const testOwner = "user-42"

// RepositoryContract прогоняет по реализации app.Repository всё, что порт
// обещает вызывающему.
//
// newRepository обязан отдавать пустое хранилище: подтесты друг о друге не
// знают и общего состояния не переживут. Аргумент *testing.T нужен реализации,
// которой есть что закрыть, — она повесит t.Cleanup сама.
//
// Средства конкретного двойника — подменённые ошибки, хуки, счётчики — в набор
// не входят: это инструмент теста, а не обещание порта.
func RepositoryContract(t *testing.T, newRepository func(t *testing.T) app.Repository) {
	t.Helper()

	contract := []struct {
		name string
		run  func(t *testing.T, repo app.Repository)
	}{
		{"сохранённая задача поднимается целиком", contractRoundTrip},
		{"задача без срока поднимается без срока", contractRoundTripWithoutDueDate},
		{"выполненная задача сохраняет момент завершения", contractRoundTripCompleted},
		{"задачи не путаются между собой", contractTasksDoNotMix},
		{"несуществующая задача не находится", contractGetMissing},
		{"обновление заменяет состояние", contractUpdateReplacesState},

		{"задача первой версии вставляется", contractInsertFresh},
		{"задача, изменённая до первой записи, вставляется", contractInsertMutated},
		{"вставка на занятое место отвергается", contractInsertOverExisting},
		{"запись исчезнувшей задачи отвергается", contractSaveVanished},
		{"второй редактор получает конфликт", contractSecondWriterConflicts},
		{"конфликт не меняет хранилище", contractConflictLeavesStorageIntact},
		{"несколько мутаций записываются разом", contractManyMutationsOneSave},
		{"потерянного обновления не происходит", contractNoLostUpdate},
		{"повторное сохранение той же версии отвергается", contractRepeatedSaveRejected},

		{"мутация поднятой задачи не видна без Save", contractLoadedTaskIsolated},
		{"мутация сохранённой задачи не видна хранилищу", contractSavedTaskIsolated},
		{"необязательные поля не разделяются указателем", contractOptionalFieldsCloned},

		{"Get отменённого запроса", contractGetRespectsCancel},
		{"Save отменённого запроса ничего не пишет", contractSaveRespectsCancel},
		{"вместо задачи передали nil", contractRejectsNilTask},

		{"из параллельных редакторов побеждает ровно один", contractConcurrentWriters},
		{"чтение и запись идут параллельно без гонок", contractConcurrentReadWrite},
	}

	for _, c := range contract {
		t.Run(c.name, func(t *testing.T) {
			repo := newRepository(t)
			if repo == nil {
				t.Fatal("newRepository вернул nil")
			}
			c.run(t, repo)
		})
	}
}

// contractRoundTrip: записанная задача поднимается тем же состоянием.
func contractRoundTrip(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	got, _, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	// Порт запрещает пару (nil, nil): вызывающий разыменует задачу сразу
	// после проверки ошибки.
	if got == nil {
		t.Fatal("Get(...) вернул nil без ошибки")
	}

	want, have := task.Snapshot(), got.Snapshot()

	if have.ID != want.ID {
		t.Errorf("id = %s, ожидался %s", have.ID, want.ID)
	}
	if have.OwnerID != want.OwnerID {
		t.Errorf("владелец = %s, ожидался %s", have.OwnerID, want.OwnerID)
	}
	if have.Title != want.Title {
		t.Errorf("заголовок = %q, ожидался %q", have.Title, want.Title)
	}
	if have.Description != want.Description {
		t.Errorf("описание = %q, ожидалось %q", have.Description, want.Description)
	}
	if have.Status != want.Status {
		t.Errorf("статус = %s, ожидался %s", have.Status, want.Status)
	}
	if have.Priority != want.Priority {
		t.Errorf("приоритет = %s, ожидался %s", have.Priority, want.Priority)
	}
	if have.Version != want.Version {
		t.Errorf("версия = %d, ожидалась %d", have.Version, want.Version)
	}
	if have.DueDate == nil {
		t.Fatal("срок потерян")
	}
	// Моменты времени сравниваются только через Equal: == у time.Time сличает
	// внутреннее представление, а не момент.
	if !have.DueDate.Time().Equal(want.DueDate.Time()) {
		t.Errorf("срок = %s, ожидался %s", have.DueDate.Time(), want.DueDate.Time())
	}
	if !have.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("createdAt = %s, ожидалось %s", have.CreatedAt, want.CreatedAt)
	}
	if !have.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("updatedAt = %s, ожидалось %s", have.UpdatedAt, want.UpdatedAt)
	}
}

// contractRoundTripWithoutDueDate: отсутствие срока — тоже состояние, и
// хранилище не вправе придумать его на подъёме.
func contractRoundTripWithoutDueDate(t *testing.T, repo app.Repository) {
	task, err := todo.NewTask(
		todotest.MustTaskID(t),
		todotest.MustOwnerID(t, testOwner),
		todotest.MustTitle(t, "Позвонить в сервис"),
		todotest.MustDescription(t, ""),
		todo.PriorityHigh,
		nil,
		testNow,
	)
	if err != nil {
		t.Fatalf("NewTask(...) вернул ошибку: %v", err)
	}
	mustSave(t, repo, task, 0)

	got := mustGet(t, repo, task.ID())
	if _, ok := got.DueDate(); ok {
		t.Error("у задачи появился срок, которого не было")
	}
}

// contractRoundTripCompleted: момент завершения переживает запись —
// восстановление не сводится к созданию новой задачи.
func contractRoundTripCompleted(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	if err := task.Complete(testLater); err != nil {
		t.Fatalf("Complete(...) вернул ошибку: %v", err)
	}
	mustSave(t, repo, task, 0)

	got := mustGet(t, repo, task.ID())

	completedAt, ok := got.CompletedAt()
	if !ok {
		t.Fatal("момент завершения потерян")
	}
	if !completedAt.Equal(testLater) {
		t.Errorf("completedAt = %s, ожидалось %s", completedAt, testLater)
	}
	if got.Status() != todo.StatusCompleted {
		t.Errorf("статус = %s, ожидался %s", got.Status(), todo.StatusCompleted)
	}
}

// contractTasksDoNotMix: задачи хранятся раздельно по идентификатору.
func contractTasksDoNotMix(t *testing.T, repo app.Repository) {
	first, second := todotest.NewTask(t, testOwner, testNow), todotest.NewTask(t, testOwner, testNow)
	mustRename(t, second, "Купить кефир", testLater)

	mustSave(t, repo, first, 0)
	mustSave(t, repo, second, 0)

	got := mustGet(t, repo, first.ID())
	if got.Title() != first.Title() {
		t.Errorf("заголовок = %q, ожидался %q", got.Title(), first.Title())
	}
}

// contractGetMissing: отсутствие задачи — это ErrTaskNotFound и никакой задачи.
func contractGetMissing(t *testing.T, repo app.Repository) {
	got, version, err := repo.Get(t.Context(), todotest.MustTaskID(t))
	if !errors.Is(err, app.ErrTaskNotFound) {
		t.Fatalf("ожидалась ErrTaskNotFound, получено: %v", err)
	}
	if got != nil {
		t.Error("вместе с ошибкой возвращена задача")
	}
	if version != 0 {
		t.Errorf("версия = %d, ожидалась 0", version)
	}
}

// contractUpdateReplacesState: повторная запись заменяет состояние целиком.
func contractUpdateReplacesState(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	stored, storedVersion, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	mustRename(t, stored, "Купить кефир", testLater)
	mustSave(t, repo, stored, storedVersion)

	got := mustGet(t, repo, task.ID())
	if got.Title().String() != "Купить кефир" {
		t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить кефир")
	}
	if got.Version() != task.Version()+1 {
		t.Errorf("версия = %d, ожидалась %d", got.Version(), task.Version()+1)
	}
	if !got.UpdatedAt().Equal(testLater) {
		t.Errorf("updatedAt = %s, ожидалось %s", got.UpdatedAt(), testLater)
	}
}

// contractInsertFresh: вставка — это loadedVersion == 0 и пустое место.
func contractInsertFresh(t *testing.T, repo app.Repository) {
	mustSave(t, repo, todotest.NewTask(t, testOwner, testNow), 0)
}

// contractInsertMutated: агрегат не обязан сохраняться после каждой мутации.
// «Создать, начать, записать один раз» — законный сценарий, и запрещать его
// хранилище не вправе.
func contractInsertMutated(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustRename(t, task, "Купить кефир", testLater)
	if err := task.Start(testLater); err != nil {
		t.Fatalf("Start(...) вернул ошибку: %v", err)
	}
	mustSave(t, repo, task, 0)

	got := mustGet(t, repo, task.ID())
	if got.Version() != task.Version() {
		t.Errorf("версия = %d, ожидалась %d", got.Version(), task.Version())
	}
	if got.Status() != todo.StatusInProgress {
		t.Errorf("статус = %s, ожидался %s", got.Status(), todo.StatusInProgress)
	}
}

// contractInsertOverExisting: писатель считает задачу новой, а место занято.
// Это конфликт, а не перезапись: тот, кто вставляет, чужого состояния не читал.
func contractInsertOverExisting(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	// Другой писатель с тем же идентификатором и своим представлением о задаче.
	twin := reconstitute(t, task.Snapshot())
	mustRename(t, twin, "Купить кефир", testLater)

	if err := repo.Save(t.Context(), twin, 0); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
	}
}

// contractSaveVanished: задачу поднимали из хранилища, а сейчас её там нет.
// Записать её как есть нельзя — версия подъёма ни с чем не сходится.
func contractSaveVanished(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustRename(t, task, "Купить кефир", testLater)

	if err := repo.Save(t.Context(), task, task.Version()-1); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
	}
	if _, _, err := repo.Get(t.Context(), task.ID()); !errors.Is(err, app.ErrTaskNotFound) {
		t.Errorf("отвергнутая запись всё-таки сохранила задачу: %v", err)
	}
}

// contractSecondWriterConflicts: из двух редакторов одной версии второй
// получает отказ.
func contractSecondWriterConflicts(t *testing.T, repo app.Repository) {
	first, second, loaded := twoWriters(t, repo)

	mustRename(t, first, "Купить кефир", testLater)
	mustSave(t, repo, first, loaded)

	mustRename(t, second, "Купить ряженку", testLater)
	if err := repo.Save(t.Context(), second, loaded); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
	}
}

// contractConflictLeavesStorageIntact: отвергнутая запись не оставляет следов.
func contractConflictLeavesStorageIntact(t *testing.T, repo app.Repository) {
	first, second, loaded := twoWriters(t, repo)

	mustRename(t, first, "Купить кефир", testLater)
	mustSave(t, repo, first, loaded)

	mustRename(t, second, "Купить ряженку", testLater)
	_ = repo.Save(t.Context(), second, loaded)

	got := mustGet(t, repo, first.ID())
	if got.Title().String() != "Купить кефир" {
		t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить кефир")
	}
	if got.Version() != first.Version() {
		t.Errorf("версия = %d, ожидалась %d", got.Version(), first.Version())
	}
}

// contractManyMutationsOneSave: пока никто другой не писал, накопить мутации
// и записать их одной операцией законно. Число мутаций между Get и Save
// ничем не ограничено.
func contractManyMutationsOneSave(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	stored, storedVersion, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	for _, title := range []string{"Купить кефир", "Купить ряженку", "Купить простоквашу"} {
		mustRename(t, stored, title, testLater)
	}
	mustSave(t, repo, stored, storedVersion)

	got := mustGet(t, repo, task.ID())
	if got.Title().String() != "Купить простоквашу" {
		t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить простоквашу")
	}
	if got.Version() != stored.Version() {
		t.Errorf("версия = %d, ожидалась %d", got.Version(), stored.Version())
	}
}

// contractNoLostUpdate: писатель, накопивший две мутации, не должен затирать
// чужую запись, сделанную между его Get и Save. Правило «на единицу больше»
// здесь и ломается — сходится с чужой версией и молча её теряет.
func contractNoLostUpdate(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	first, firstVersion, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	second, secondVersion, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}

	// Второй редактор успевает записать одну мутацию.
	mustRename(t, second, "Версия второго", testLater)
	mustSave(t, repo, second, secondVersion)

	// Первый накопил две.
	mustRename(t, first, "Версия первого", testLater)
	if err := first.Start(testLater); err != nil {
		t.Fatalf("Start(...) вернул ошибку: %v", err)
	}
	if err := repo.Save(t.Context(), first, firstVersion); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
	}

	got := mustGet(t, repo, task.ID())
	if got.Title().String() != "Версия второго" {
		t.Errorf("заголовок = %q, ожидался %q — чужая запись потеряна", got.Title(), "Версия второго")
	}
}

// contractRepeatedSaveRejected: версия не выросла — значит с прошлой записи
// ничего не произошло, и это не обновление, а повтор.
func contractRepeatedSaveRejected(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	if err := repo.Save(t.Context(), task, 0); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("ожидалась ErrVersionConflict, получено: %v", err)
	}
}

// contractLoadedTaskIsolated: изменения поднятой задачи не попадают в
// хранилище в обход Save.
func contractLoadedTaskIsolated(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	first := mustGet(t, repo, task.ID())
	mustRename(t, first, "Купить кефир", testLater)

	second := mustGet(t, repo, task.ID())
	if second.Title() != task.Title() {
		t.Errorf("заголовок = %q, ожидался %q", second.Title(), task.Title())
	}
	if second.Version() != task.Version() {
		t.Errorf("версия = %d, ожидалась %d", second.Version(), task.Version())
	}
}

// contractSavedTaskIsolated: объект, отданный в Save, продолжает жить у
// вызывающего, и его дальнейшие мутации хранилища не касаются.
func contractSavedTaskIsolated(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	mustRename(t, task, "Купить кефир", testLater)

	got := mustGet(t, repo, task.ID())
	if got.Title().String() != "Купить молоко" {
		t.Errorf("заголовок = %q, ожидался %q", got.Title(), "Купить молоко")
	}
}

// contractOptionalFieldsCloned: присваивание структуры в Go копирует
// указатель, а не значение, поэтому две поднятые задачи не должны смотреть
// на один *DueDate. Сравнивать указатели из двух Snapshot() бесполезно —
// снимок клонирует их при каждом вызове, — поэтому проверка поведенческая:
// меняем через одну копию, смотрим на другую.
func contractOptionalFieldsCloned(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	first := mustGet(t, repo, task.ID())
	second := mustGet(t, repo, task.ID())

	wantDue, ok := second.DueDate()
	if !ok {
		t.Fatal("у поднятой задачи нет срока")
	}

	newDue, err := todo.NewDueDate(testDue.Add(72*time.Hour), testNow)
	if err != nil {
		t.Fatalf("NewDueDate(...) вернул ошибку: %v", err)
	}
	if err := first.Reschedule(&newDue, testNow); err != nil {
		t.Fatalf("Reschedule(...) вернул ошибку: %v", err)
	}

	gotDue, ok := second.DueDate()
	if !ok {
		t.Fatal("срок второй копии исчез")
	}
	if !gotDue.Time().Equal(wantDue.Time()) {
		t.Errorf("срок второй копии = %s, ожидался %s — состояние разделяется",
			gotDue.Time(), wantDue.Time())
	}

	// То же про момент завершения: он появляется у той копии, которую
	// завершили, и только у неё.
	if err := first.Complete(testLater); err != nil {
		t.Fatalf("Complete(...) вернул ошибку: %v", err)
	}
	if _, ok := second.CompletedAt(); ok {
		t.Error("вторая копия завершилась вместе с первой")
	}
	if second.Status() != todo.StatusPending {
		t.Errorf("статус второй копии = %s, ожидался %s", second.Status(), todo.StatusPending)
	}
}

// contractGetRespectsCancel: отменённый запрос не обслуживается, и ошибка
// обязана донести причину до errors.Is — сценарий её не подменяет.
func contractGetRespectsCancel(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err := repo.Get(ctx, task.ID()); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено: %v", err)
	}
}

// contractSaveRespectsCancel: до записи отмена законна и останавливает работу.
func contractSaveRespectsCancel(t *testing.T, repo app.Repository) {
	task := todotest.NewTask(t, testOwner, testNow)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := repo.Save(ctx, task, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено: %v", err)
	}
	if _, _, err := repo.Get(t.Context(), task.ID()); !errors.Is(err, app.ErrTaskNotFound) {
		t.Errorf("отменённый запрос всё-таки записал задачу: %v", err)
	}
}

// contractRejectsNilTask: хранилище стоит на границе, и мусор с той стороны
// обязано пережить — ошибкой, а не паникой.
func contractRejectsNilTask(t *testing.T, repo app.Repository) {
	if err := repo.Save(t.Context(), nil, 0); err == nil {
		t.Fatal("Save(nil) не вернул ошибку")
	}
}

// contractConcurrentWriters: из редакторов, поднявших одну версию,
// записывается ровно один — остальные получают конфликт.
func contractConcurrentWriters(t *testing.T, repo app.Repository) {
	const editorCount = 8

	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	// Каждый редактор поднимает задачу заранее: все стартуют с одной и той же
	// версии, как несколько человек, открывших одну карточку.
	type editor struct {
		task   *todo.Task
		loaded int
	}

	editors := make([]editor, editorCount)
	for i := range editors {
		got, loaded, err := repo.Get(t.Context(), task.ID())
		if err != nil {
			t.Fatalf("Get(...) вернул ошибку: %v", err)
		}
		mustRename(t, got, "Купить кефир", testLater)
		editors[i] = editor{task: got, loaded: loaded}
	}

	var succeeded, conflicted atomic.Int64

	var wg sync.WaitGroup
	wg.Add(editorCount)
	for _, e := range editors {
		go func() {
			defer wg.Done()

			switch err := repo.Save(t.Context(), e.task, e.loaded); {
			case err == nil:
				succeeded.Add(1)
			case errors.Is(err, app.ErrVersionConflict):
				conflicted.Add(1)
			default:
				t.Errorf("Save(...) вернул неожиданную ошибку: %v", err)
			}
		}()
	}
	wg.Wait()

	if succeeded.Load() != 1 {
		t.Errorf("успешных записей %d, ожидалась 1", succeeded.Load())
	}
	if conflicted.Load() != editorCount-1 {
		t.Errorf("конфликтов %d, ожидалось %d", conflicted.Load(), editorCount-1)
	}

	got := mustGet(t, repo, task.ID())
	if got.Version() != task.Version()+1 {
		t.Errorf("версия = %d, ожидалась %d", got.Version(), task.Version()+1)
	}
}

// contractConcurrentReadWrite: чтение и запись не мешают друг другу.
// Проверка имеет смысл под -race и без него ничего не доказывает.
func contractConcurrentReadWrite(t *testing.T, repo app.Repository) {
	const readers = 8

	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	writer, writerVersion, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	mustRename(t, writer, "Купить кефир", testLater)

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	go func() {
		defer wg.Done()

		if err := repo.Save(t.Context(), writer, writerVersion); err != nil {
			t.Errorf("Save(...) вернул ошибку: %v", err)
		}
	}()

	for range readers {
		go func() {
			defer wg.Done()

			if _, _, err := repo.Get(t.Context(), task.ID()); err != nil {
				t.Errorf("Get(...) вернул ошибку: %v", err)
			}
		}()
	}
	wg.Wait()
}

// twoWriters кладёт задачу в хранилище и поднимает её дважды: обе копии
// подняты из одного состояния — это и есть два параллельных редактора,
// каждый со своей версией правды.
func twoWriters(t *testing.T, repo app.Repository) (first, second *todo.Task, loaded int) {
	t.Helper()

	task := todotest.NewTask(t, testOwner, testNow)
	mustSave(t, repo, task, 0)

	first, loaded, err := repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	second, _, err = repo.Get(t.Context(), task.ID())
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	return first, second, loaded
}

// mustSave записывает задачу или валит тест.
func mustSave(t *testing.T, repo app.Repository, task *todo.Task, loadedVersion int) {
	t.Helper()

	if err := repo.Save(t.Context(), task, loadedVersion); err != nil {
		t.Fatalf("Save(..., %d) вернул ошибку: %v", loadedVersion, err)
	}
}

// mustGet поднимает задачу или валит тест.
func mustGet(t *testing.T, repo app.Repository, id todo.TaskID) *todo.Task {
	t.Helper()

	task, _, err := repo.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(...) вернул ошибку: %v", err)
	}
	if task == nil {
		t.Fatal("Get(...) вернул nil без ошибки")
	}
	return task
}

// reconstitute поднимает задачу из снимка или валит тест.
func reconstitute(t *testing.T, snapshot todo.TaskSnapshot) *todo.Task {
	t.Helper()

	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		t.Fatalf("ReconstituteTask(...) вернул ошибку: %v", err)
	}
	return task
}

// mustRename переименовывает задачу или валит тест — типовая мутация,
// которой набор двигает версию.
func mustRename(t *testing.T, task *todo.Task, title string, now time.Time) {
	t.Helper()

	if err := task.Rename(todotest.MustTitle(t, title), now); err != nil {
		t.Fatalf("Rename(%q) вернул ошибку: %v", title, err)
	}
}
