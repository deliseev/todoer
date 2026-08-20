package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/domain/todo"
	"github.com/deliseev/todoer/internal/transport/httpapi"
)

// Опорные моменты времени. Транспорт о времени не думает — он его только
// печатает и разбирает, — поэтому здесь достаточно констант.
var (
	testNow = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	testDue = testNow.Add(24 * time.Hour)
)

const (
	testOwner = "user-42"
	// testTaskID — валидный идентификатор задачи: 32 шестнадцатеричных знака.
	testTaskID = "0123456789abcdef0123456789abcdef"
	// testCursor — курсор в том виде, в каком его отдаёт сценарий. Транспорт
	// в него не заглядывает и заглядывать не должен: для него это строка,
	// которую он переносит от ответа к следующему запросу.
	testCursor = "eyJzIjoiZHVlIiwiaSI6IjAxMjMifQ"
)

// mustTaskID разбирает опорный идентификатор задачи или валит тест.
//
// Транспорт доменных типов не знает, но двойник сценария отдаёт todo.TaskID —
// ровно как настоящий CreateTask.
func mustTaskID(t *testing.T) todo.TaskID {
	t.Helper()

	id, err := todo.ParseTaskID(testTaskID)
	if err != nil {
		t.Fatalf("ParseTaskID(%q) вернул ошибку: %v", testTaskID, err)
	}
	return id
}

// testView — типовой ответ сценария чтения.
func testView() app.TaskView {
	due := testDue
	return app.TaskView{
		ID:          testTaskID,
		OwnerID:     testOwner,
		Title:       "Купить молоко",
		Description: "Два литра, в магазине у дома",
		Status:      "pending",
		Priority:    "normal",
		DueDate:     &due,
		CreatedAt:   testNow,
		UpdatedAt:   testNow,
		Version:     1,
	}
}

// testPage — типовой ответ сценария списка: одна задача и курсор дальше.
func testPage() app.TaskPage {
	return app.TaskPage{
		Tasks:      []app.TaskView{testView()},
		NextCursor: testCursor,
	}
}

// newTestServer собирает транспорт на двойнике сценариев.
func newTestServer(t *testing.T, service *fakeService) http.Handler {
	t.Helper()

	handler, err := httpapi.NewHandler(service)
	if err != nil {
		t.Fatalf("NewHandler(...) вернул ошибку: %v", err)
	}
	return handler.Routes()
}

// do выполняет запрос к транспорту и возвращает ответ.
//
// Владелец проставляется заголовком по умолчанию: подавляющее большинство
// проверок не про него. Запросы без владельца собираются вручную.
func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := newRequest(t, method, target, body)
	req.Header.Set(ownerHeader, testOwner)
	return send(h, req)
}

// newRequest готовит запрос без заголовка владельца.
func newRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()

	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// send прогоняет готовый запрос через обработчик.
func send(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ownerHeader и idempotencyHeader дублируют константы транспорта: тесты живут
// в httpapi_test и внутренностей пакета не видят — как и всякий его клиент.
// Заодно опечатка в имени заголовка перестаёт сходиться сама с собой.
const (
	ownerHeader       = "X-Owner-ID"
	idempotencyHeader = "Idempotency-Key"
)

// decodeBody разбирает тело ответа в карту.
//
// Именно в карту, а не в структуру транспорта: тест, читающий ответ теми же
// тегами, которыми его писали, пропустил бы опечатку в теге — она сошлась бы
// сама с собой. Клиент видит имена полей, вот их и проверяем.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("тело ответа не разобралось (%q): %v", rec.Body.String(), err)
	}
	return body
}

// requireStatus валит тест, если код ответа не тот.
func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("код ответа = %d, ожидался %d (тело: %s)", rec.Code, want, rec.Body.String())
	}
}

// requireJSONContentType проверяет, что ответ помечен как JSON.
func requireJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	got := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, ожидался application/json", got)
	}
}
