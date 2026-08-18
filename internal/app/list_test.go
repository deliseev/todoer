package app_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/domain/todo/todotest"
)

// listQuery — запрос списка от законного владельца.
func listQuery() app.ListTasksQuery {
	return app.ListTasksQuery{OwnerID: testOwner}
}

// testViews собирает count представлений, различимых по всем ключам
// сортировки: сроку, приоритету и моменту создания.
func testViews(t *testing.T, count int) []app.TaskView {
	t.Helper()

	views := make([]app.TaskView, count)
	for i := range views {
		due := testDue.Add(time.Duration(i) * time.Hour)
		views[i] = app.TaskView{
			ID:        todotest.MustTaskID(t).String(),
			OwnerID:   testOwner,
			Title:     "Задача " + strconv.Itoa(i),
			Status:    todo.StatusPending.String(),
			Priority:  todo.PriorityHigh.String(),
			DueDate:   &due,
			CreatedAt: testNow.Add(time.Duration(i) * time.Minute),
			UpdatedAt: testNow.Add(time.Duration(i) * time.Minute),
			Version:   1,
		}
	}

	return views
}

func TestListTasksParsesQuery(t *testing.T) {
	t.Run("статус и приоритет разбираются в значимые объекты", func(t *testing.T) {
		// Транспорт присылает строки, значимые объекты собирает сценарий:
		// иначе разбор доменных значений расползётся по каждому транспорту.
		env := newTestEnv(t)

		query := listQuery()
		query.Status = "in_progress"
		query.Priority = "critical"

		if _, err := env.service.ListTasks(t.Context(), query); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		filter := env.queries.lastFilter(t)
		if filter.OwnerID.String() != testOwner {
			t.Errorf("владелец = %s, ожидался %s", filter.OwnerID, testOwner)
		}
		if filter.Status == nil || *filter.Status != todo.StatusInProgress {
			t.Errorf("статус = %v, ожидался %v", filter.Status, todo.StatusInProgress)
		}
		if filter.Priority == nil || *filter.Priority != todo.PriorityCritical {
			t.Errorf("приоритет = %v, ожидался %v", filter.Priority, todo.PriorityCritical)
		}
	})

	t.Run("пустой отбор означает «любой»", func(t *testing.T) {
		// Пустая строка — это «не фильтровать», а не «фильтровать нулевым
		// значением»: у статуса и приоритета нулевые значения осмысленны,
		// и спутать эти два случая значило бы молча отдать не тот список.
		env := newTestEnv(t)

		if _, err := env.service.ListTasks(t.Context(), listQuery()); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		filter := env.queries.lastFilter(t)
		if filter.Status != nil {
			t.Errorf("статус = %v, ожидалось отсутствие отбора", *filter.Status)
		}
		if filter.Priority != nil {
			t.Errorf("приоритет = %v, ожидалось отсутствие отбора", *filter.Priority)
		}
		if filter.DueFrom != nil || filter.DueTo != nil {
			t.Errorf("окно по сроку = [%v, %v), ожидалось отсутствие отбора", filter.DueFrom, filter.DueTo)
		}
	})

	t.Run("негодный запрос до хранилища не доходит", func(t *testing.T) {
		// То же правило, что у команд: разбор идёт до обращения к базе,
		// и негодный запрос не стоит ни одного чтения.
		cases := []struct {
			name    string
			mutate  func(q *app.ListTasksQuery)
			wantErr error
		}{
			{
				name:    "владелец пуст",
				mutate:  func(q *app.ListTasksQuery) { q.OwnerID = "   " },
				wantErr: todo.ErrInvalidOwnerID,
			},
			{
				name:    "неизвестный статус",
				mutate:  func(q *app.ListTasksQuery) { q.Status = "почти сделано" },
				wantErr: todo.ErrUnknownStatus,
			},
			{
				name:    "неизвестный приоритет",
				mutate:  func(q *app.ListTasksQuery) { q.Priority = "очень срочно" },
				wantErr: todo.ErrUnknownPriority,
			},
			{
				name:    "неизвестная сортировка",
				mutate:  func(q *app.ListTasksQuery) { q.Sort = "title" },
				wantErr: app.ErrInvalidListQuery,
			},
			{
				name:    "неизвестный отбор по сроку",
				mutate:  func(q *app.ListTasksQuery) { q.Due = "завтра" },
				wantErr: app.ErrInvalidListQuery,
			},
			{
				name:    "лимит не число",
				mutate:  func(q *app.ListTasksQuery) { q.Limit = "много" },
				wantErr: app.ErrInvalidListQuery,
			},
			{
				name:    "лимит меньше единицы",
				mutate:  func(q *app.ListTasksQuery) { q.Limit = "0" },
				wantErr: app.ErrInvalidListQuery,
			},
			{
				name: "лимит сверх потолка",
				// Отказ, а не молчаливое обрезание: клиент, попросивший
				// тысячу и получивший сотню, не узнает, что список неполон,
				// и решит, что задачи кончились.
				mutate:  func(q *app.ListTasksQuery) { q.Limit = strconv.Itoa(app.MaxListLimit + 1) },
				wantErr: app.ErrInvalidListQuery,
			},
			{
				name:    "курсор не разбирается",
				mutate:  func(q *app.ListTasksQuery) { q.After = "не курсор" },
				wantErr: app.ErrInvalidListQuery,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newTestEnv(t)

				query := listQuery()
				tc.mutate(&query)

				if _, err := env.service.ListTasks(t.Context(), query); !errors.Is(err, tc.wantErr) {
					t.Fatalf("ожидалась %v, получено: %v", tc.wantErr, err)
				}
				if calls := env.queries.callCount(); calls != 0 {
					t.Errorf("обращений к хранилищу %d, ожидалось 0", calls)
				}
			})
		}
	})
}

func TestListTasksDueWindow(t *testing.T) {
	// «Сегодня» и «просрочено» считает сценарий по часам, а не SQL функцией
	// now(): база тогда завела бы себе второе представление о «сейчас»,
	// и тест перестал бы быть воспроизводимым. Хранилище получает абсолютные
	// моменты и ни о каком «сегодня» не знает.
	t.Run("сегодня — сутки от начала дня", func(t *testing.T) {
		env := newTestEnv(t)

		query := listQuery()
		query.Due = "today"

		if _, err := env.service.ListTasks(t.Context(), query); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		filter := env.queries.lastFilter(t)
		wantFrom := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)

		if filter.DueFrom == nil || !filter.DueFrom.Equal(wantFrom) {
			t.Errorf("начало окна = %v, ожидалось %s", filter.DueFrom, wantFrom)
		}
		if filter.DueTo == nil || !filter.DueTo.Equal(wantFrom.AddDate(0, 0, 1)) {
			t.Errorf("конец окна = %v, ожидалось %s", filter.DueTo, wantFrom.AddDate(0, 0, 1))
		}
	})

	t.Run("просрочено — всё до текущего момента", func(t *testing.T) {
		env := newTestEnv(t)
		env.clock.set(testLater)

		query := listQuery()
		query.Due = "overdue"

		if _, err := env.service.ListTasks(t.Context(), query); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		filter := env.queries.lastFilter(t)
		if filter.DueFrom != nil {
			t.Errorf("начало окна = %v, ожидалось отсутствие границы", filter.DueFrom)
		}
		if filter.DueTo == nil || !filter.DueTo.Equal(testLater) {
			t.Errorf("конец окна = %v, ожидался %s", filter.DueTo, testLater)
		}
	})
}

func TestListTasksSort(t *testing.T) {
	t.Run("по умолчанию — по сроку, ближайший первым", func(t *testing.T) {
		env := newTestEnv(t)

		if _, err := env.service.ListTasks(t.Context(), listQuery()); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		filter := env.queries.lastFilter(t)
		if filter.Sort != app.SortByDueDate {
			t.Errorf("сортировка = %v, ожидалась по сроку", filter.Sort)
		}
		if filter.Descending {
			t.Error("порядок убывающий, ожидался возрастающий")
		}
	})

	t.Run("минус означает убывание", func(t *testing.T) {
		cases := []struct {
			value          string
			wantSort       app.TaskSort
			wantDescending bool
		}{
			{value: "due", wantSort: app.SortByDueDate},
			{value: "-due", wantSort: app.SortByDueDate, wantDescending: true},
			{value: "priority", wantSort: app.SortByPriority},
			{value: "-priority", wantSort: app.SortByPriority, wantDescending: true},
			{value: "created", wantSort: app.SortByCreatedAt},
			{value: "-created", wantSort: app.SortByCreatedAt, wantDescending: true},
		}

		for _, tc := range cases {
			t.Run(tc.value, func(t *testing.T) {
				env := newTestEnv(t)

				query := listQuery()
				query.Sort = tc.value

				if _, err := env.service.ListTasks(t.Context(), query); err != nil {
					t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
				}

				filter := env.queries.lastFilter(t)
				if filter.Sort != tc.wantSort {
					t.Errorf("сортировка = %v, ожидалась %v", filter.Sort, tc.wantSort)
				}
				if filter.Descending != tc.wantDescending {
					t.Errorf("убывание = %v, ожидалось %v", filter.Descending, tc.wantDescending)
				}
			})
		}
	})
}

func TestListTasksPaging(t *testing.T) {
	t.Run("у хранилища запрашивается на строку больше просимого", func(t *testing.T) {
		// Лишняя строка — единственный честный способ узнать, есть ли
		// следующая страница. Без неё пришлось бы отдавать курсор всегда
		// и заставлять клиента ходить за пустой страницей, чтобы понять,
		// что список кончился.
		env := newTestEnv(t)

		query := listQuery()
		query.Limit = "2"

		if _, err := env.service.ListTasks(t.Context(), query); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		if got := env.queries.lastFilter(t).Limit; got != 3 {
			t.Errorf("лимит запроса = %d, ожидалось 3", got)
		}
	})

	t.Run("лимит по умолчанию", func(t *testing.T) {
		env := newTestEnv(t)

		if _, err := env.service.ListTasks(t.Context(), listQuery()); err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		if got := env.queries.lastFilter(t).Limit; got != app.DefaultListLimit+1 {
			t.Errorf("лимит запроса = %d, ожидалось %d", got, app.DefaultListLimit+1)
		}
	})

	t.Run("лишняя строка не уезжает клиенту, но даёт курсор", func(t *testing.T) {
		env := newTestEnv(t)
		views := testViews(t, 3)
		env.queries.returns(views...)

		query := listQuery()
		query.Limit = "2"

		page, err := env.service.ListTasks(t.Context(), query)
		if err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		if len(page.Tasks) != 2 {
			t.Fatalf("отдано %d задач, ожидалось 2", len(page.Tasks))
		}
		if page.Tasks[1].ID != views[1].ID {
			t.Errorf("вторая задача = %s, ожидалась %s", page.Tasks[1].ID, views[1].ID)
		}
		if page.NextCursor == "" {
			t.Error("курсор пуст, хотя следующая страница есть")
		}
	})

	t.Run("последняя страница курсора не несёт", func(t *testing.T) {
		env := newTestEnv(t)
		env.queries.returns(testViews(t, 2)...)

		query := listQuery()
		query.Limit = "2"

		page, err := env.service.ListTasks(t.Context(), query)
		if err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		if len(page.Tasks) != 2 {
			t.Fatalf("отдано %d задач, ожидалось 2", len(page.Tasks))
		}
		if page.NextCursor != "" {
			t.Errorf("курсор = %q, ожидался пустой: следующей страницы нет", page.NextCursor)
		}
	})

	t.Run("курсор указывает на последнюю отданную задачу", func(t *testing.T) {
		// Круг замыкается внутри сценария: он же курсор выдал, он же его
		// и разбирает. Клиент видит непрозрачную строку и знать о её
		// устройстве не обязан.
		env := newTestEnv(t)
		views := testViews(t, 3)
		env.queries.returns(views...)

		query := listQuery()
		query.Limit = "2"

		page, err := env.service.ListTasks(t.Context(), query)
		if err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		next := listQuery()
		next.Limit = "2"
		next.After = page.NextCursor

		if _, err := env.service.ListTasks(t.Context(), next); err != nil {
			t.Fatalf("ListTasks(...) со страницы вернул ошибку: %v", err)
		}

		after := env.queries.lastFilter(t).After
		if after == nil {
			t.Fatal("курсор не доехал до хранилища")
		}
		if after.ID.String() != views[1].ID {
			t.Errorf("курсор указывает на %s, ожидалась %s", after.ID, views[1].ID)
		}
		if after.DueDate == nil || !after.DueDate.Equal(*views[1].DueDate) {
			t.Errorf("срок в курсоре = %v, ожидался %s", after.DueDate, views[1].DueDate)
		}
		if !after.CreatedAt.Equal(views[1].CreatedAt) {
			t.Errorf("момент создания в курсоре = %s, ожидался %s", after.CreatedAt, views[1].CreatedAt)
		}
		if after.PriorityRank != todo.PriorityHigh.Rank() {
			t.Errorf("ранг в курсоре = %d, ожидался %d", after.PriorityRank, todo.PriorityHigh.Rank())
		}
	})

	t.Run("курсор от другой сортировки отвергается", func(t *testing.T) {
		// Курсор — это место в порядке, а не в наборе. Продолжи мы по нему
		// другую сортировку, клиент молча получил бы выборку, не значащую
		// ничего: ни продолжение, ни начало.
		env := newTestEnv(t)
		env.queries.returns(testViews(t, 3)...)

		query := listQuery()
		query.Limit = "2"

		page, err := env.service.ListTasks(t.Context(), query)
		if err != nil {
			t.Fatalf("ListTasks(...) вернул ошибку: %v", err)
		}

		next := listQuery()
		next.Limit = "2"
		next.After = page.NextCursor
		next.Sort = "priority"

		if _, err := env.service.ListTasks(t.Context(), next); !errors.Is(err, app.ErrInvalidListQuery) {
			t.Fatalf("ожидалась ErrInvalidListQuery, получено: %v", err)
		}
	})

	t.Run("отказ хранилища уезжает вызывающему", func(t *testing.T) {
		env := newTestEnv(t)
		storeErr := errors.New("база недоступна")
		env.queries.fail(storeErr)

		if _, err := env.service.ListTasks(t.Context(), listQuery()); !errors.Is(err, storeErr) {
			t.Fatalf("ожидалась ошибка хранилища, получено: %v", err)
		}
	})
}
