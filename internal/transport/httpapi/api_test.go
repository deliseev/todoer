package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/transport/httpapi"
)

func TestNewHandlerRejectsMissingService(t *testing.T) {
	if _, err := httpapi.NewHandler(nil); !errors.Is(err, app.ErrMissingDependency) {
		t.Fatalf("ожидалась ErrMissingDependency, получено: %v", err)
	}
}

func TestRouting(t *testing.T) {
	t.Run("неизвестный путь", func(t *testing.T) {
		rec := do(t, newTestServer(t, newFakeService()), http.MethodGet, "/nope", "")
		requireStatus(t, rec, http.StatusNotFound)
	})

	t.Run("метод не тот", func(t *testing.T) {
		// Мультиплексор net/http сам различает «нет пути» и «нет метода».
		rec := do(t, newTestServer(t, newFakeService()), http.MethodDelete, "/tasks/"+testTaskID, "")
		requireStatus(t, rec, http.StatusMethodNotAllowed)
	})
}

func TestOwnerHeader(t *testing.T) {
	// Владелец приезжает заголовком, и без него запрос не запрос: сценарию
	// нечего спрашивать. Отказ обязан случиться до похода в app.
	requests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"создание", http.MethodPost, "/tasks", `{"title":"Купить молоко"}`},
		{"чтение", http.MethodGet, "/tasks/" + testTaskID, ""},
		{"изменение", http.MethodPatch, "/tasks/" + testTaskID, `{"title":"Купить кефир"}`},
		{"взятие в работу", http.MethodPost, "/tasks/" + testTaskID + "/start", ""},
		{"выполнение", http.MethodPost, "/tasks/" + testTaskID + "/complete", ""},
		{"отмена", http.MethodPost, "/tasks/" + testTaskID + "/cancel", ""},
	}

	for _, r := range requests {
		t.Run(r.name+" без заголовка", func(t *testing.T) {
			service := newFakeService()
			service.view = testView()

			rec := send(newTestServer(t, service), newRequest(t, r.method, r.target, r.body))

			// 400, а не 401: схемы аутентификации нет, а 401 обязывает
			// прислать WWW-Authenticate. Заголовок здесь — часть формы
			// запроса, и его отсутствие — негодный запрос.
			requireStatus(t, rec, http.StatusBadRequest)
			requireJSONContentType(t, rec)
			if got := service.totalCalls(); got != 0 {
				t.Errorf("обращений к сценариям %d, ожидалось 0", got)
			}
		})

		t.Run(r.name+" с пустым заголовком", func(t *testing.T) {
			service := newFakeService()
			req := newRequest(t, r.method, r.target, r.body)
			req.Header.Set(ownerHeader, "   ")

			requireStatus(t, send(newTestServer(t, service), req), http.StatusBadRequest)
			if got := service.totalCalls(); got != 0 {
				t.Errorf("обращений к сценариям %d, ожидалось 0", got)
			}
		})
	}
}

func TestRequestBodyLimit(t *testing.T) {
	// Тело ограничено сверху, и отказ обязан случиться до похода в app.
	// Транспорт разбирает раньше, чем сценарий проверит длину, поэтому без
	// потолка гигабайтный заголовок целиком оказался бы в памяти — и только
	// потом получил бы 400 за длину.
	requests := []struct {
		name   string
		method string
		target string
	}{
		{"создание", http.MethodPost, "/tasks"},
		{"изменение", http.MethodPatch, "/tasks/" + testTaskID},
	}

	// Заведомо больше потолка: законное тело ограничено доменом (заголовок
	// и описание вместе — тысячи рун, не мегабайты).
	huge := `{"title":"` + strings.Repeat("a", 2<<20) + `"}`

	for _, r := range requests {
		t.Run(r.name, func(t *testing.T) {
			service := newFakeService()
			service.view = testView()

			rec := do(t, newTestServer(t, service), r.method, r.target, huge)

			// 413, а не 400: дело не в форме запроса, а в его размере.
			requireStatus(t, rec, http.StatusRequestEntityTooLarge)
			requireJSONContentType(t, rec)
			if got := service.totalCalls(); got != 0 {
				t.Errorf("обращений к сценариям %d, ожидалось 0 — огромное тело дошло до app", got)
			}
		})
	}
}

func TestStrictBodyDecoding(t *testing.T) {
	// Разбор тела строгий, и отказ случается до похода в app. Молчаливо
	// проглоченное поле — худший из возможных ответов: клиент получает 200
	// и уверен, что его правка уехала, а она выброшена по дороге.
	routes := []struct {
		name   string
		method string
		target string
	}{
		{"создание", http.MethodPost, "/tasks"},
		{"изменение", http.MethodPatch, "/tasks/" + testTaskID},
	}

	bodies := []struct {
		name string
		body string
	}{
		{
			// Опечатка в имени поля: без строгости PATCH ответил бы 200
			// с неизменённым описанием.
			name: "опечатка в имени поля",
			body: `{"title":"Купить кефир","descriptoin":"Два литра"}`,
		},
		{
			// Второй документ в теле молча пропадал бы, а клиент считал бы,
			// что прислал оба.
			name: "хвост после документа",
			body: `{"title":"Купить кефир"} {"title":"Купить ряженку"}`,
		},
	}

	for _, route := range routes {
		for _, body := range bodies {
			t.Run(route.name+"/"+body.name, func(t *testing.T) {
				service := newFakeService()
				service.view = testView()

				rec := do(t, newTestServer(t, service), route.method, route.target, body.body)

				requireStatus(t, rec, http.StatusBadRequest)
				requireJSONContentType(t, rec)
				if got := service.totalCalls(); got != 0 {
					t.Errorf("обращений к сценариям %d, ожидалось 0 — негодное тело дошло до app", got)
				}
			})
		}
	}
}

func TestCreateTask(t *testing.T) {
	t.Run("задача создаётся", func(t *testing.T) {
		service := newFakeService()
		service.createID = mustTaskID(t)
		service.view = testView()

		rec := do(t, newTestServer(t, service), http.MethodPost, "/tasks", `{
			"title": "Купить молоко",
			"description": "Два литра, в магазине у дома",
			"priority": "high",
			"due_date": "2026-08-14T12:00:00Z"
		}`)

		requireStatus(t, rec, http.StatusCreated)
		requireJSONContentType(t, rec)

		// Location указывает на созданный ресурс — иначе клиенту негде взять
		// идентификатор, кроме как разбирая тело.
		if want := "/tasks/" + service.createID.String(); rec.Header().Get("Location") != want {
			t.Errorf("Location = %q, ожидался %q", rec.Header().Get("Location"), want)
		}

		// Тело несёт идентификатор созданной задачи, и только его: остальное
		// клиент уже знает — он это и прислал, а статус, версию и времена
		// назначает домен, и за ними придётся сходить отдельно.
		body := decodeBody(t, rec)
		if got := body["id"]; got != service.createID.String() {
			t.Errorf("id в ответе = %#v, ожидался %q", got, service.createID.String())
		}
		if len(body) != 1 {
			t.Errorf("полей в ответе %d, ожидалось 1 (%v)", len(body), body)
		}

		// Команда собрана из тела и заголовка, а не откуда-то ещё.
		cmd := service.createCmd
		if cmd.OwnerID != testOwner {
			t.Errorf("владелец в команде = %q, ожидался %q", cmd.OwnerID, testOwner)
		}
		if cmd.Title != "Купить молоко" {
			t.Errorf("заголовок в команде = %q, ожидался %q", cmd.Title, "Купить молоко")
		}
		if cmd.Description != "Два литра, в магазине у дома" {
			t.Errorf("описание в команде = %q", cmd.Description)
		}
		if cmd.Priority != "high" {
			t.Errorf("приоритет в команде = %q, ожидался %q", cmd.Priority, "high")
		}
		if cmd.DueDate == nil {
			t.Fatal("срок в команду не попал")
		}
		if !cmd.DueDate.Equal(testDue) {
			t.Errorf("срок в команде = %s, ожидался %s", cmd.DueDate, testDue)
		}
	})

	t.Run("задача без срока", func(t *testing.T) {
		service := newFakeService()
		service.createID = mustTaskID(t)
		service.view = testView()

		rec := do(t, newTestServer(t, service), http.MethodPost, "/tasks",
			`{"title":"Купить молоко"}`)

		requireStatus(t, rec, http.StatusCreated)
		if service.createCmd.DueDate != nil {
			t.Errorf("срок = %s, ожидалось отсутствие", service.createCmd.DueDate)
		}
		// Пустой приоритет — законный вход: в CreateTaskCommand это обычный
		// приоритет, и подставлять "normal" транспорту не за чем.
		if service.createCmd.Priority != "" {
			t.Errorf("приоритет = %q, ожидалась пустая строка", service.createCmd.Priority)
		}
	})

	t.Run("битый JSON", func(t *testing.T) {
		service := newFakeService()

		rec := do(t, newTestServer(t, service), http.MethodPost, "/tasks", `{"title":`)

		requireStatus(t, rec, http.StatusBadRequest)
		if got := service.totalCalls(); got != 0 {
			t.Errorf("обращений к сценариям %d, ожидалось 0 — битое тело дошло до app", got)
		}
	})

	t.Run("срок не по RFC 3339", func(t *testing.T) {
		service := newFakeService()

		rec := do(t, newTestServer(t, service), http.MethodPost, "/tasks",
			`{"title":"Купить молоко","due_date":"14.08.2026"}`)

		requireStatus(t, rec, http.StatusBadRequest)
		if got := service.totalCalls(); got != 0 {
			t.Errorf("обращений к сценариям %d, ожидалось 0", got)
		}
	})

	t.Run("отказ доставки событий — всё равно успех", func(t *testing.T) {
		// Задача записана и видна всем, кто её прочтёт. 5xx заставил бы
		// клиента повторить запись, которая уже состоялась, а POST /tasks
		// неидемпотентен — на повторе появилась бы вторая задача.
		// Отказ доставки уезжает в лог, а не в ответ.
		service := newFakeService()
		service.createID = mustTaskID(t)
		service.view = testView()
		service.createErr = &app.EventDeliveryError{
			TaskID: service.createID,
			Err:    errors.New("publisher: unavailable"),
		}

		rec := do(t, newTestServer(t, service), http.MethodPost, "/tasks",
			`{"title":"Купить молоко"}`)

		requireStatus(t, rec, http.StatusCreated)
		if want := "/tasks/" + service.createID.String(); rec.Header().Get("Location") != want {
			t.Errorf("Location = %q, ожидался %q", rec.Header().Get("Location"), want)
		}
	})
}

func TestGetTask(t *testing.T) {
	t.Run("задача отдаётся целиком", func(t *testing.T) {
		service := newFakeService()
		service.view = testView()

		rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/"+testTaskID, "")

		requireStatus(t, rec, http.StatusOK)
		requireJSONContentType(t, rec)

		body := decodeBody(t, rec)
		want := map[string]any{
			"id":           testTaskID,
			"owner_id":     testOwner,
			"title":        "Купить молоко",
			"description":  "Два литра, в магазине у дома",
			"status":       "pending",
			"priority":     "normal",
			"due_date":     "2026-08-14T12:00:00Z",
			"created_at":   "2026-08-13T12:00:00Z",
			"updated_at":   "2026-08-13T12:00:00Z",
			"completed_at": nil,
			"version":      float64(1),
		}
		for key, wantValue := range want {
			got, ok := body[key]
			if !ok {
				t.Errorf("поля %q нет в ответе", key)
				continue
			}
			if got != wantValue {
				t.Errorf("%s = %#v, ожидалось %#v", key, got, wantValue)
			}
		}
		// Лишних полей быть не должно: ответ — контракт, а не свалка.
		if len(body) != len(want) {
			t.Errorf("полей в ответе %d, ожидалось %d (%v)", len(body), len(want), body)
		}

		// Запрос собран из пути и заголовка.
		if service.getQuery.TaskID != testTaskID {
			t.Errorf("id в запросе = %q, ожидался %q", service.getQuery.TaskID, testTaskID)
		}
		if service.getQuery.OwnerID != testOwner {
			t.Errorf("владелец в запросе = %q, ожидался %q", service.getQuery.OwnerID, testOwner)
		}
	})

	t.Run("выполненная задача несёт момент завершения", func(t *testing.T) {
		service := newFakeService()
		view := testView()
		completed := testNow.Add(time.Hour)
		view.Status = "completed"
		view.CompletedAt = &completed
		service.view = view

		rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/"+testTaskID, "")

		requireStatus(t, rec, http.StatusOK)
		if got := decodeBody(t, rec)["completed_at"]; got != "2026-08-13T13:00:00Z" {
			t.Errorf("completed_at = %#v", got)
		}
	})

	t.Run("задача без срока отдаёт null", func(t *testing.T) {
		// Именно null, а не пропущенное поле: клиент должен видеть разницу
		// между «срока нет» и «поле не пришло».
		service := newFakeService()
		view := testView()
		view.DueDate = nil
		service.view = view

		rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/"+testTaskID, "")

		requireStatus(t, rec, http.StatusOK)
		body := decodeBody(t, rec)
		got, ok := body["due_date"]
		if !ok {
			t.Fatal("поле due_date пропало из ответа")
		}
		if got != nil {
			t.Errorf("due_date = %#v, ожидался null", got)
		}
	})

	t.Run("чужая задача неотличима от несуществующей", func(t *testing.T) {
		// Транспорту не надо помнить про маскировку 403 в 404: сценарий уже
		// отдал ErrTaskNotFound, и различать тут нечего.
		service := newFakeService()
		service.getErr = app.ErrTaskNotFound

		rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/"+testTaskID, "")

		requireStatus(t, rec, http.StatusNotFound)
		requireJSONContentType(t, rec)
	})

	t.Run("негодный идентификатор", func(t *testing.T) {
		service := newFakeService()
		service.getErr = todo.ErrInvalidTaskID

		rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/xyz", "")

		requireStatus(t, rec, http.StatusBadRequest)
	})
}

func TestUpdateTask(t *testing.T) {
	t.Run("частичное изменение", func(t *testing.T) {
		service := newFakeService()
		service.view = testView()

		rec := do(t, newTestServer(t, service), http.MethodPatch, "/tasks/"+testTaskID,
			`{"title":"Купить кефир"}`)

		requireStatus(t, rec, http.StatusOK)

		cmd := service.updateCmd
		if cmd.TaskID != testTaskID || cmd.OwnerID != testOwner {
			t.Errorf("id/владелец в команде = %q/%q", cmd.TaskID, cmd.OwnerID)
		}
		if cmd.Title == nil || *cmd.Title != "Купить кефир" {
			t.Errorf("заголовок в команде = %v", cmd.Title)
		}
		// Присланного нет — значит не трогать. Пустая строка вместо nil
		// стёрла бы описание запросом, который его не упоминал.
		if cmd.Description != nil {
			t.Errorf("описание в команде = %v, ожидался nil", *cmd.Description)
		}
		if cmd.Priority != nil {
			t.Errorf("приоритет в команде = %v, ожидался nil", *cmd.Priority)
		}
		if cmd.DueDate != nil {
			t.Errorf("срок в команде = %v, ожидался nil", cmd.DueDate)
		}

		// Ответ — обновлённая задача, значит после записи сценарий чтения
		// вызван ровно один раз.
		if got := service.callCount("GetTask"); got != 1 {
			t.Errorf("обращений к GetTask %d, ожидалось 1", got)
		}
	})

	t.Run("пустое описание — это очистка, а не пропуск", func(t *testing.T) {
		service := newFakeService()
		service.view = testView()

		do(t, newTestServer(t, service), http.MethodPatch, "/tasks/"+testTaskID,
			`{"description":""}`)

		cmd := service.updateCmd
		if cmd.Description == nil {
			t.Fatal("описание не доехало: пустая строка — законное значение")
		}
		if *cmd.Description != "" {
			t.Errorf("описание = %q, ожидалась пустая строка", *cmd.Description)
		}
	})

	t.Run("срок: три состояния", func(t *testing.T) {
		// Ради этого DueDate в команде — *DueDateUpdate, а не указатель
		// на время: у поля три состояния, и стандартный json.Unmarshal
		// сам их не различает — и отсутствие, и null дают нулевой указатель.
		cases := []struct {
			name  string
			body  string
			check func(*testing.T, *app.DueDateUpdate)
		}{
			{
				name: "поля нет — не трогать",
				body: `{"title":"Купить кефир"}`,
				check: func(t *testing.T, got *app.DueDateUpdate) {
					if got != nil {
						t.Errorf("DueDate = %v, ожидался nil", got)
					}
				},
			},
			{
				name: "null — снять срок",
				body: `{"due_date":null}`,
				check: func(t *testing.T, got *app.DueDateUpdate) {
					if got == nil {
						t.Fatal("DueDate = nil, ожидалось снятие срока")
					}
					if got.At != nil {
						t.Errorf("At = %s, ожидался nil", got.At)
					}
				},
			},
			{
				name: "значение — назначить",
				body: `{"due_date":"2026-08-14T12:00:00Z"}`,
				check: func(t *testing.T, got *app.DueDateUpdate) {
					if got == nil || got.At == nil {
						t.Fatal("срок не доехал")
					}
					if !got.At.Equal(testDue) {
						t.Errorf("At = %s, ожидалось %s", got.At, testDue)
					}
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				service := newFakeService()
				service.view = testView()

				do(t, newTestServer(t, service), http.MethodPatch, "/tasks/"+testTaskID, tc.body)

				// Без этого случай «поля нет» сходится сам с собой: у нетронутого
				// двойника DueDate и так nil, и проверка не отличила бы верный
				// разбор от невызванного хендлера.
				if got := service.callCount("UpdateTask"); got != 1 {
					t.Fatalf("обращений к UpdateTask %d, ожидалось 1", got)
				}

				tc.check(t, service.updateCmd.DueDate)
			})
		}
	})

	t.Run("команда без полей", func(t *testing.T) {
		service := newFakeService()
		service.updateErr = app.ErrEmptyUpdate

		rec := do(t, newTestServer(t, service), http.MethodPatch, "/tasks/"+testTaskID, `{}`)

		requireStatus(t, rec, http.StatusBadRequest)
	})

	t.Run("конфликт версий", func(t *testing.T) {
		service := newFakeService()
		service.updateErr = app.ErrVersionConflict

		rec := do(t, newTestServer(t, service), http.MethodPatch, "/tasks/"+testTaskID,
			`{"title":"Купить кефир"}`)

		requireStatus(t, rec, http.StatusConflict)
	})

	t.Run("отказ чтения после успешной записи", func(t *testing.T) {
		// Запись состоялась, а перечитать не вышло. Врать про 404 нельзя:
		// задача есть. Отвечаем 500 — клиент перечитает сам.
		service := newFakeService()
		service.getErr = errors.New("storage unavailable")

		rec := do(t, newTestServer(t, service), http.MethodPatch, "/tasks/"+testTaskID,
			`{"title":"Купить кефир"}`)

		requireStatus(t, rec, http.StatusInternalServerError)
		if got := service.callCount("UpdateTask"); got != 1 {
			t.Errorf("обращений к UpdateTask %d, ожидалось 1", got)
		}
	})
}

func TestStatusActions(t *testing.T) {
	actions := []struct {
		name    string
		path    string
		call    string
		failure func(*fakeService, error)
	}{
		{
			name:    "start",
			path:    "/start",
			call:    "StartTask",
			failure: func(s *fakeService, err error) { s.startErr = err },
		},
		{
			name:    "complete",
			path:    "/complete",
			call:    "CompleteTask",
			failure: func(s *fakeService, err error) { s.doneErr = err },
		},
		{
			name:    "cancel",
			path:    "/cancel",
			call:    "CancelTask",
			failure: func(s *fakeService, err error) { s.cancelErr = err },
		},
	}

	for _, a := range actions {
		target := "/tasks/" + testTaskID + a.path

		t.Run(a.name+"/успех", func(t *testing.T) {
			service := newFakeService()
			service.view = testView()

			rec := do(t, newTestServer(t, service), http.MethodPost, target, "")

			requireStatus(t, rec, http.StatusOK)
			requireJSONContentType(t, rec)
			if got := service.callCount(a.call); got != 1 {
				t.Errorf("обращений к %s %d, ожидалось 1", a.call, got)
			}
			if got := decodeBody(t, rec)["id"]; got != testTaskID {
				t.Errorf("id в ответе = %#v", got)
			}
		})

		t.Run(a.name+"/запрещённый переход", func(t *testing.T) {
			// 409, а не 400: тот же запрос был бы законен, застань он задачу
			// в другом статусе. Дело не в форме запроса, а в состоянии.
			service := newFakeService()
			a.failure(service, todo.ErrInvalidStatusTransition)

			requireStatus(t, do(t, newTestServer(t, service), http.MethodPost, target, ""),
				http.StatusConflict)
		})

		t.Run(a.name+"/задача выполнена", func(t *testing.T) {
			service := newFakeService()
			a.failure(service, todo.ErrTaskAlreadyCompleted)

			requireStatus(t, do(t, newTestServer(t, service), http.MethodPost, target, ""),
				http.StatusConflict)
		})

		t.Run(a.name+"/задачи нет", func(t *testing.T) {
			service := newFakeService()
			a.failure(service, app.ErrTaskNotFound)

			requireStatus(t, do(t, newTestServer(t, service), http.MethodPost, target, ""),
				http.StatusNotFound)
		})
	}
}

func TestErrorMapping(t *testing.T) {
	// Единственное место, где транспорт знает про сентинели. Таблица общая,
	// чтобы новый сентинель попадал под проверку одной строкой, а не тремя
	// разными тестами.
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"задачи нет", app.ErrTaskNotFound, http.StatusNotFound},
		{"конфликт версий", app.ErrVersionConflict, http.StatusConflict},
		{"пустая команда", app.ErrEmptyUpdate, http.StatusBadRequest},
		{"негодный идентификатор", todo.ErrInvalidTaskID, http.StatusBadRequest},
		{"пустой заголовок", todo.ErrEmptyTitle, http.StatusBadRequest},
		{"описание слишком длинное", todo.ErrDescriptionTooLong, http.StatusBadRequest},
		{"неизвестный приоритет", todo.ErrUnknownPriority, http.StatusBadRequest},
		{"срок в прошлом", todo.ErrDueDateInPast, http.StatusBadRequest},
		{"запрещённый переход", todo.ErrInvalidStatusTransition, http.StatusConflict},
		{"задача выполнена", todo.ErrTaskAlreadyCompleted, http.StatusConflict},
		{"задача отменена", todo.ErrTaskCancelled, http.StatusConflict},
		{"запрос отменён", context.Canceled, http.StatusRequestTimeout},
		{"неопознанная ошибка", errors.New("boom"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := newFakeService()
			service.getErr = tc.err

			rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/"+testTaskID, "")

			requireStatus(t, rec, tc.want)
			requireJSONContentType(t, rec)

			// Тело отказа — всегда объект с полем error, и оно не пустое:
			// пустой текст в логе бесполезен.
			body := decodeBody(t, rec)
			message, ok := body["error"].(string)
			if !ok {
				t.Fatalf("в ответе нет строкового поля error: %v", body)
			}
			if message == "" {
				t.Error("текст ошибки пуст")
			}
		})
	}

	t.Run("внутренняя ошибка не утекает наружу", func(t *testing.T) {
		// Текст неопознанной ошибки может нести имя таблицы, путь или адрес
		// соседнего сервиса. Клиенту достаточно кода.
		service := newFakeService()
		service.getErr = errors.New("dial tcp 10.0.0.7:5432: connection refused")

		rec := do(t, newTestServer(t, service), http.MethodGet, "/tasks/"+testTaskID, "")

		requireStatus(t, rec, http.StatusInternalServerError)
		if message, _ := decodeBody(t, rec)["error"].(string); strings.Contains(message, "10.0.0.7") {
			t.Errorf("ответ раскрыл внутренности: %q", message)
		}
	})
}
