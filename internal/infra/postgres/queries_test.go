package postgres_test

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
	"github.com/deliseev/todoer/internal/infra/postgres"
)

// Сторона чтения обязана удовлетворять порту.
var _ app.TaskQueries = (*postgres.TaskQueries)(nil)

// taskSpec — задача, которую тест кладёт в базу.
//
// Отдельной структурой, потому что списку важно ровно то, по чему он отбирает
// и сортирует: владелец, статус, приоритет, срок и момент создания.
type taskSpec struct {
	owner     string
	status    todo.Status
	priority  todo.Priority
	due       *time.Time
	createdAt time.Time
}

// TestTaskQueriesFilters: отбор — то, чего порт обещает, а проверить можно
// только на настоящей базе: двойник сценариев не фильтрует ничего.
func TestTaskQueriesFilters(t *testing.T) {
	t.Parallel()

	t.Run("чужие задачи не видны", func(t *testing.T) {
		t.Parallel()

		// Владелец входит в фильтр, а не проверяется после выборки: обойти
		// его хранилищу нечем, и это единственная защита списка от чужого.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		mine := seedTask(t, pool, taskSpec{owner: testOwner})
		seedTask(t, pool, taskSpec{owner: "user-13"})

		views := list(t, queries, filterFor(t, testOwner))

		if len(views) != 1 {
			t.Fatalf("отдано %d задач, ожидалась 1", len(views))
		}
		if views[0].ID != mine {
			t.Errorf("отдана задача %s, ожидалась %s", views[0].ID, mine)
		}
	})

	t.Run("отбор по статусу", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		seedTask(t, pool, taskSpec{owner: testOwner, status: todo.StatusPending})
		started := seedTask(t, pool, taskSpec{owner: testOwner, status: todo.StatusInProgress})

		filter := filterFor(t, testOwner)
		filter.Status = new(todo.StatusInProgress)

		views := list(t, queries, filter)

		if len(views) != 1 {
			t.Fatalf("отдано %d задач, ожидалась 1", len(views))
		}
		if views[0].ID != started {
			t.Errorf("отдана задача %s, ожидалась %s", views[0].ID, started)
		}
	})

	t.Run("отбор по приоритету", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityNormal})
		urgent := seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityCritical})

		filter := filterFor(t, testOwner)
		filter.Priority = new(todo.PriorityCritical)

		views := list(t, queries, filter)

		if len(views) != 1 {
			t.Fatalf("отдано %d задач, ожидалась 1", len(views))
		}
		if views[0].ID != urgent {
			t.Errorf("отдана задача %s, ожидалась %s", views[0].ID, urgent)
		}
	})

	t.Run("окно по сроку полуоткрыто", func(t *testing.T) {
		t.Parallel()

		// Обе границы разом: задача ровно на нижней границе входит, ровно
		// на верхней — нет. Ошибись хранилище в любой из них, и «сегодня»
		// показывало бы вчерашнее или прятало сегодняшнее.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		from := testNow
		to := testNow.Add(24 * time.Hour)
		before := from.Add(-time.Second)
		inside := from.Add(time.Hour)

		seedTask(t, pool, taskSpec{owner: testOwner, due: &before})
		atFrom := seedTask(t, pool, taskSpec{owner: testOwner, due: &from})
		within := seedTask(t, pool, taskSpec{owner: testOwner, due: &inside})
		seedTask(t, pool, taskSpec{owner: testOwner, due: &to})
		seedTask(t, pool, taskSpec{owner: testOwner})

		filter := filterFor(t, testOwner)
		filter.DueFrom, filter.DueTo = &from, &to

		got := idsOf(list(t, queries, filter))
		want := []string{atFrom, within}

		if !slices.Equal(got, want) {
			t.Errorf("отданы задачи %v, ожидались %v", got, want)
		}
	})
}

// TestTaskQueriesOrder: порядок выдачи.
func TestTaskQueriesOrder(t *testing.T) {
	t.Parallel()

	t.Run("по сроку, задачи без срока в конце", func(t *testing.T) {
		t.Parallel()

		// Задача без срока — не задача с нулевым сроком: NULL в ключе
		// сортировки обязан получить определённое место, иначе порядок
		// становится делом случая, а курсору не с чем сравниваться.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		later := testNow.Add(48 * time.Hour)
		sooner := testNow.Add(24 * time.Hour)

		third := seedTask(t, pool, taskSpec{owner: testOwner})
		second := seedTask(t, pool, taskSpec{owner: testOwner, due: &later})
		first := seedTask(t, pool, taskSpec{owner: testOwner, due: &sooner})

		got := idsOf(list(t, queries, filterFor(t, testOwner)))

		if !slices.Equal(got, []string{first, second, third}) {
			t.Errorf("порядок %v, ожидался %v", got, []string{first, second, third})
		}
	})

	t.Run("по приоритету — рангом, а не именем", func(t *testing.T) {
		t.Parallel()

		// По алфавиту critical шло бы раньше high, а low раньше normal —
		// то есть порядок, не значащий ничего.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		high := seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityHigh})
		low := seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityLow})
		critical := seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityCritical})
		normal := seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityNormal})

		filter := filterFor(t, testOwner)
		filter.Sort = app.SortByPriority

		got := idsOf(list(t, queries, filter))

		if !slices.Equal(got, []string{low, normal, high, critical}) {
			t.Errorf("порядок %v, ожидался %v", got, []string{low, normal, high, critical})
		}
	})

	t.Run("по моменту создания", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		second := seedTask(t, pool, taskSpec{owner: testOwner, createdAt: testNow.Add(time.Hour)})
		first := seedTask(t, pool, taskSpec{owner: testOwner, createdAt: testNow})

		filter := filterFor(t, testOwner)
		filter.Sort = app.SortByCreatedAt

		got := idsOf(list(t, queries, filter))

		if !slices.Equal(got, []string{first, second}) {
			t.Errorf("порядок %v, ожидался %v", got, []string{first, second})
		}
	})

	t.Run("убывание переворачивает порядок целиком", func(t *testing.T) {
		t.Parallel()

		// Целиком — вместе с разрешением ничьих: перевернись только ключ,
		// задачи с одинаковым сроком поехали бы по возрастанию идентификатора
		// внутри убывающей выдачи, и курсор через них не прошёл бы.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		due := testNow.Add(24 * time.Hour)
		ids := []string{
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
		}

		filter := filterFor(t, testOwner)
		ascending := idsOf(list(t, queries, filter))

		filter.Descending = true
		descending := idsOf(list(t, queries, filter))

		if len(ascending) != len(ids) || len(descending) != len(ids) {
			t.Fatalf("отдано %d и %d задач, ожидалось по %d", len(ascending), len(descending), len(ids))
		}
		for i := range ascending {
			if ascending[i] != descending[len(descending)-1-i] {
				t.Fatalf("убывающий порядок %v не обратен возрастающему %v", descending, ascending)
			}
		}
	})

	t.Run("ничьи разрешает идентификатор", func(t *testing.T) {
		t.Parallel()

		// Одинаковый срок у трёх задач — не редкость: «сегодня к вечеру»
		// ставят пачкой. Без разрешения ничьих порядок между ними менялся бы
		// от запроса к запросу, а страница теряла бы и повторяла задачи.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		due := testNow.Add(24 * time.Hour)
		for range 5 {
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due})
		}

		first := idsOf(list(t, queries, filterFor(t, testOwner)))
		second := idsOf(list(t, queries, filterFor(t, testOwner)))

		if !slices.Equal(first, second) {
			t.Errorf("порядок между запросами изменился: %v и %v", first, second)
		}
		if !slices.IsSorted(first) {
			t.Errorf("ничьи разрешены не идентификатором: %v", first)
		}
	})
}

// TestTaskQueriesPaging: страницы по курсору.
func TestTaskQueriesPaging(t *testing.T) {
	t.Parallel()

	t.Run("лимит соблюдается", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		for range 5 {
			seedTask(t, pool, taskSpec{owner: testOwner})
		}

		filter := filterFor(t, testOwner)
		filter.Limit = 2

		if views := list(t, queries, filter); len(views) != 2 {
			t.Errorf("отдано %d задач, ожидалось 2", len(views))
		}
	})

	t.Run("проход по курсору не теряет и не повторяет задачи", func(t *testing.T) {
		t.Parallel()

		// То, ради чего курсор и выбран вместо OFFSET. Сроки нарочно
		// повторяются, а часть задач их не имеет вовсе: и ничьи, и NULL —
		// это места, где страница рвётся, если сравнение неполное.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		due := testNow.Add(24 * time.Hour)
		other := testNow.Add(48 * time.Hour)
		want := []string{
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
			seedTask(t, pool, taskSpec{owner: testOwner, due: &other}),
			seedTask(t, pool, taskSpec{owner: testOwner}),
			seedTask(t, pool, taskSpec{owner: testOwner}),
		}

		got := walkPages(t, queries, filterFor(t, testOwner), 2)

		if len(got) != len(want) {
			t.Fatalf("проход отдал %d задач, ожидалось %d: %v", len(got), len(want), got)
		}
		if !sameSet(got, want) {
			t.Errorf("проход отдал %v, ожидались те же задачи, что и разом: %v", got, want)
		}
	})

	t.Run("проход по курсору в убывающем порядке", func(t *testing.T) {
		t.Parallel()

		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		due := testNow.Add(24 * time.Hour)
		want := []string{
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
			seedTask(t, pool, taskSpec{owner: testOwner, due: &due}),
			seedTask(t, pool, taskSpec{owner: testOwner}),
			seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityHigh}),
		}

		filter := filterFor(t, testOwner)
		filter.Descending = true

		got := walkPages(t, queries, filter, 2)

		if len(got) != len(want) || !sameSet(got, want) {
			t.Errorf("проход отдал %v, ожидались %v", got, want)
		}
	})

	t.Run("курсор продолжает и сортировку по приоритету", func(t *testing.T) {
		t.Parallel()

		// Ключ здесь целочисленный, а не момент времени, и сравнение у него
		// своё: проверяется, что курсор работает для каждой сортировки,
		// а не только для той, что по умолчанию.
		pool := newPool(t)
		queries := postgres.NewTaskQueries(pool)

		want := []string{
			seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityLow}),
			seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityHigh}),
			seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityHigh}),
			seedTask(t, pool, taskSpec{owner: testOwner, priority: todo.PriorityCritical}),
		}

		filter := filterFor(t, testOwner)
		filter.Sort = app.SortByPriority

		got := walkPages(t, queries, filter, 2)

		if len(got) != len(want) || !sameSet(got, want) {
			t.Errorf("проход отдал %v, ожидались %v", got, want)
		}
	})
}

// seedTask кладёт в базу задачу по описанию и отдаёт её идентификатор.
//
// Через боевое хранилище, а не запросом на месте: список читает те же
// колонки, которые пишет TaskRepository, — priority_rank в том числе, —
// и заполняй их тест сам, он проверял бы собственную засыпку.
func seedTask(t *testing.T, pool *pgxpool.Pool, spec taskSpec) string {
	t.Helper()

	// Нулевые значения статуса и приоритета осмысленны, поэтому умолчания
	// нужны только там, где ноль не значит ничего.
	owner := spec.owner
	if owner == "" {
		owner = testOwner
	}
	createdAt := spec.createdAt
	if createdAt.IsZero() {
		createdAt = testNow
	}

	snapshot := todo.TaskSnapshot{
		ID:          todotest.MustTaskID(t),
		OwnerID:     todotest.MustOwnerID(t, owner),
		Title:       todotest.MustTitle(t, "Купить молоко"),
		Description: todotest.MustDescription(t, "Два литра, в магазине у дома"),
		Status:      spec.status,
		Priority:    spec.priority,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
		Version:     1,
	}
	if spec.due != nil {
		// Восстановлением, а не обычной фабрикой: тесту нужны и просроченные
		// сроки, а «срок в будущем» — правило создания.
		due := todotest.MustReconstituteDueDate(t, *spec.due)
		snapshot.DueDate = &due
	}

	task, err := todo.ReconstituteTask(snapshot)
	if err != nil {
		t.Fatalf("ReconstituteTask(...) вернул ошибку: %v", err)
	}
	if err := postgres.NewTaskRepository(pool).Save(t.Context(), task, 0); err != nil {
		t.Fatalf("Save(...) вернул ошибку: %v", err)
	}

	return snapshot.ID.String()
}

// filterFor — фильтр по умолчанию: все задачи владельца одной страницей.
//
// Лимит заведомо больше, чем кладут тесты: где важна страница, его задают
// на месте.
func filterFor(t *testing.T, owner string) app.TaskFilter {
	t.Helper()

	return app.TaskFilter{
		OwnerID: todotest.MustOwnerID(t, owner),
		Sort:    app.SortByDueDate,
		Limit:   100,
	}
}

// list читает страницу или валит тест.
func list(t *testing.T, queries *postgres.TaskQueries, filter app.TaskFilter) []app.TaskView {
	t.Helper()

	views, err := queries.List(t.Context(), filter)
	if err != nil {
		t.Fatalf("List(...) вернул ошибку: %v", err)
	}

	return views
}

// idsOf выписывает идентификаторы страницы: порядок и состав так читаются
// в диагностике одной строкой.
func idsOf(views []app.TaskView) []string {
	ids := make([]string, len(views))
	for i, view := range views {
		ids[i] = view.ID
	}

	return ids
}

// sameSet сравнивает выдачи как множества — там, где важен состав, а не
// порядок.
func sameSet(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

// walkPages проходит список страницами по pageSize и собирает всё, что тот
// отдал.
//
// Курсор строится так же, как его строит сценарий, — из последней отданной
// строки, — потому что проверяется здесь именно продолжение с него.
func walkPages(t *testing.T, queries *postgres.TaskQueries, filter app.TaskFilter, pageSize int) []string {
	t.Helper()

	filter.Limit = pageSize

	var ids []string

	// Потолок проходов, а не while: ошибка в сравнении с курсором иначе
	// обернулась бы вечным циклом вместо упавшего теста.
	for range 100 {
		views := list(t, queries, filter)
		ids = append(ids, idsOf(views)...)

		// Неполная страница — последняя: спрашивать следующую не за чем.
		if len(views) < pageSize {
			return ids
		}

		cursor := cursorTo(t, views[len(views)-1], filter.Sort)
		filter.After = &cursor
	}

	t.Fatalf("проход по страницам не кончился, собрано %d задач", len(ids))

	return nil
}

// cursorTo собирает курсор на последнюю отданную задачу.
func cursorTo(t *testing.T, view app.TaskView, sort app.TaskSort) app.TaskCursor {
	t.Helper()

	id, err := todo.ParseTaskID(view.ID)
	if err != nil {
		t.Fatalf("ParseTaskID(%q) вернул ошибку: %v", view.ID, err)
	}
	priority, err := todo.ParsePriority(view.Priority)
	if err != nil {
		t.Fatalf("ParsePriority(%q) вернул ошибку: %v", view.Priority, err)
	}

	return app.TaskCursor{
		Sort:         sort,
		DueDate:      view.DueDate,
		PriorityRank: priority.Rank(),
		CreatedAt:    view.CreatedAt,
		ID:           id,
	}
}
