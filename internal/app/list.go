package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/deliseev/todoer/internal/domain/todo"
)

// Именованные окна по сроку. Границы считает сценарий по часам: «сегодня» —
// это про «сейчас», а не про содержимое таблицы, и вычисли их база сама,
// у неё завелось бы второе представление о текущем моменте.
const (
	dueToday   = "today"
	dueOverdue = "overdue"
)

// ListTasks отдаёт страницу списка задач владельца.
//
// Чтение идёт мимо единицы работы: список ничего не пишет, не двигает версий
// и не порождает событий, а транзакция ради одного SELECT стоит дороже, чем
// даёт. Чужие задачи в выдачу не попадают по построению — владелец входит
// в фильтр, и обойти его хранилищу нечем.
func (s *TaskService) ListTasks(ctx context.Context, query ListTasksQuery) (TaskPage, error) {
	filter, limit, err := s.buildFilter(query)
	if err != nil {
		return TaskPage{}, err
	}

	views, err := s.queries.List(ctx, filter)
	if err != nil {
		return TaskPage{}, err
	}

	// Строк пришло не больше просимого — значит это последняя страница,
	// и курсора у неё нет: клиент останавливается по его отсутствию,
	// а не по пустой странице, за которой пришлось бы сходить.
	if len(views) <= limit {
		return TaskPage{Tasks: views}, nil
	}

	// Лишняя строка была спрошена только затем, чтобы узнать о следующей
	// странице. Клиенту она не уезжает.
	views = views[:limit]

	cursor, err := cursorTo(views[len(views)-1], filter.Sort)
	if err != nil {
		return TaskPage{}, err
	}

	return TaskPage{Tasks: views, NextCursor: cursor}, nil
}

// buildFilter разбирает запрос в фильтр и возвращает размер страницы.
//
// Весь разбор идёт до обращения к хранилищу — то же правило, что у команд:
// негодный запрос не стоит ни одного чтения.
func (s *TaskService) buildFilter(query ListTasksQuery) (TaskFilter, int, error) {
	owner, err := todo.ParseOwnerID(query.OwnerID)
	if err != nil {
		return TaskFilter{}, 0, err
	}
	status, err := parseListStatus(query.Status)
	if err != nil {
		return TaskFilter{}, 0, err
	}
	priority, err := parseListPriority(query.Priority)
	if err != nil {
		return TaskFilter{}, 0, err
	}
	sort, descending, err := parseSort(query.Sort)
	if err != nil {
		return TaskFilter{}, 0, err
	}
	limit, err := parseLimit(query.Limit)
	if err != nil {
		return TaskFilter{}, 0, err
	}

	filter := TaskFilter{
		OwnerID:    owner,
		Status:     status,
		Priority:   priority,
		Sort:       sort,
		Descending: descending,
		// На строку больше просимого: лишняя не уезжает клиенту, но говорит,
		// что следующая страница есть. Иначе о её существовании можно было бы
		// узнать только сходив за ней.
		Limit: limit + 1,
	}

	filter.DueFrom, filter.DueTo, err = dueWindow(query.Due, s.clock.Now())
	if err != nil {
		return TaskFilter{}, 0, err
	}

	if query.After != "" {
		cursor, err := decodeCursor(query.After, sort)
		if err != nil {
			return TaskFilter{}, 0, err
		}
		filter.After = &cursor
	}

	return filter, limit, nil
}

// parseListStatus разбирает отбор по статусу: пустая строка означает «любой».
//
// Здесь пустое значение читается иначе, чем в командах создания, и это
// намеренно: у todo.Status нулевое значение осмысленно, и отбор по нему —
// не то же самое, что отсутствие отбора.
func parseListStatus(value string) (*todo.Status, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	status, err := todo.ParseStatus(value)
	if err != nil {
		return nil, err
	}

	return &status, nil
}

// parseListPriority разбирает отбор по приоритету: пустая строка — «любой».
func parseListPriority(value string) (*todo.Priority, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	priority, err := todo.ParsePriority(value)
	if err != nil {
		return nil, err
	}

	return &priority, nil
}

// parseSort разбирает поле сортировки; минус впереди означает убывание.
//
// Умолчание — ближайший срок первым: список задач читают, чтобы узнать,
// что горит.
func parseSort(value string) (TaskSort, bool, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" {
		return SortByDueDate, false, nil
	}

	descending := strings.HasPrefix(name, "-")
	name = strings.TrimPrefix(name, "-")

	for i, candidate := range sortNames {
		if candidate == name {
			return TaskSort(i), descending, nil
		}
	}

	return 0, false, fmt.Errorf("app: parse list sort (%q): %w", value, ErrInvalidListQuery)
}

// parseLimit разбирает размер страницы.
//
// Превышение потолка — отказ, а не молчаливое обрезание: клиент, попросивший
// тысячу и получивший сотню, решил бы, что задачи кончились.
func parseLimit(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return DefaultListLimit, nil
	}

	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("app: parse list limit (%q): %w", value, ErrInvalidListQuery)
	}
	if limit < 1 || limit > MaxListLimit {
		return 0, fmt.Errorf("app: check list limit (%d, want 1..%d): %w",
			limit, MaxListLimit, ErrInvalidListQuery)
	}

	return limit, nil
}

// dueWindow превращает именованное окно в полуинтервал [from, to).
//
// Полуинтервал, а не пара «с» и «по»: у суток нет последнего мгновения,
// и включающая верхняя граница потребовала бы выбирать, чем считать конец
// дня — 23:59:59 или 23:59:59.999999.
func dueWindow(value string, now time.Time) (from, to *time.Time, err error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return nil, nil, nil

	case dueToday:
		// Зона — UTC: часового пояса у пользователя пока нет, и придумывать
		// его за него хуже, чем честно считать сутки по UTC.
		year, month, day := now.UTC().Date()
		start := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 0, 1)

		return &start, &end, nil

	case dueOverdue:
		// Просрочено — всё, чей срок уже прошёл. Нижней границы нет:
		// задача, просроченная год назад, просрочена не меньше вчерашней.
		return nil, &now, nil
	}

	return nil, nil, fmt.Errorf("app: parse list due filter (%q): %w", value, ErrInvalidListQuery)
}

// cursorTo собирает курсор, указывающий на отданное представление.
//
// Ключи собираются из самого представления, а не из фильтра: курсор описывает
// место в порядке, а место задаёт строка, на которой страница кончилась.
func cursorTo(view TaskView, sort TaskSort) (string, error) {
	id, err := todo.ParseTaskID(view.ID)
	if err != nil {
		return "", fmt.Errorf("app: build list cursor task id (%q): %w", view.ID, err)
	}
	priority, err := todo.ParsePriority(view.Priority)
	if err != nil {
		return "", fmt.Errorf("app: build list cursor priority (%q): %w", view.Priority, err)
	}

	return encodeCursor(TaskCursor{
		Sort:         sort,
		DueDate:      view.DueDate,
		PriorityRank: priority.Rank(),
		CreatedAt:    view.CreatedAt,
		ID:           id,
	})
}
