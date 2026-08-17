package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/infra/postgres/pgtest"
)

// TestMain готовит шаблонную базу: сборка целиком проверяется на настоящем
// хранилище, а не на памяти.
func TestMain(m *testing.M) {
	pgtest.Run(m)
}

func TestNewTaskService(t *testing.T) {
	t.Parallel()

	t.Run("боевые реализации подходят портам", func(t *testing.T) {
		t.Parallel()

		// Хранилище настоящее: боевая реализация у порта осталась одна, и
		// проверять сборку на чём-то ещё значило бы проверять не сборку.
		pool, err := postgres.Open(t.Context(), pgtest.NewDSN(t))
		if err != nil {
			t.Fatalf("подключение к базе теста: %v", err)
		}
		t.Cleanup(pool.Close)

		service, err := newTaskService(postgres.NewTaskRepository(pool))
		if err != nil {
			t.Fatalf("newTaskService() вернул ошибку: %v", err)
		}
		if service == nil {
			t.Fatal("newTaskService() вернул nil без ошибки")
		}
	})
}

func TestDatabaseURL(t *testing.T) {
	// Без t.Parallel: подтесты правят окружение процесса.

	t.Run("умолчания нет", func(t *testing.T) {
		t.Setenv(dsnEnv, "")

		if got := databaseURL(); got != "" {
			t.Errorf("databaseURL() = %q, ожидалась пустая строка", got)
		}
	})

	t.Run("из окружения", func(t *testing.T) {
		t.Setenv(dsnEnv, " postgres://localhost/todoer ")

		if got := databaseURL(); got != "postgres://localhost/todoer" {
			t.Errorf("databaseURL() = %q, ожидался адрес без пробелов", got)
		}
	})
}

func TestListenAddr(t *testing.T) {
	// Без t.Parallel: подтесты правят окружение процесса.

	t.Run("по умолчанию", func(t *testing.T) {
		t.Setenv(addrEnv, "")

		if got := listenAddr(); got != defaultAddr {
			t.Errorf("listenAddr() = %q, ожидался %q", got, defaultAddr)
		}
	})

	t.Run("из окружения", func(t *testing.T) {
		t.Setenv(addrEnv, " 127.0.0.1:9999 ")

		// Пробелы по краям — след копирования из конфига, а не часть адреса.
		if got := listenAddr(); got != "127.0.0.1:9999" {
			t.Errorf("listenAddr() = %q, ожидался %q", got, "127.0.0.1:9999")
		}
	})
}

func TestRestoreSignalsOnCancel(t *testing.T) {
	t.Parallel()

	// Первый сигнал отменяет контекст и запускает остановку, которая длится
	// до десяти секунд. Всё это время обработчик сигналов обязан быть снят:
	// иначе второй Ctrl+C нетерпеливого оператора будет проглочен — контекст
	// уже отменён, и делать этому сигналу нечего.
	restored := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())

	restoreSignalsOnCancel(ctx, func() { close(restored) })

	select {
	case <-restored:
		t.Fatal("поведение сигналов восстановлено до отмены: первый же сигнал убьёт процесс на месте")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case <-restored:
	case <-time.After(2 * time.Second):
		t.Fatal("после отмены поведение сигналов не восстановлено: второй сигнал будет проглочен")
	}
}

func TestNewServer(t *testing.T) {
	t.Parallel()

	// Нулевой таймаут у http.Server значит «ждать вечно», поэтому проверяется
	// именно ненулевость. Одного ReadHeaderTimeout мало: клиент, приславший
	// корректные заголовки и сочащий тело по байту, застревает уже в чтении
	// тела, а спящий в keep-alive — вообще ни в одной из фаз запроса.
	server := newServer(http.NewServeMux())

	timeouts := []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", server.ReadHeaderTimeout},
		{"ReadTimeout", server.ReadTimeout},
		{"WriteTimeout", server.WriteTimeout},
		{"IdleTimeout", server.IdleTimeout},
	}

	for _, timeout := range timeouts {
		if timeout.got <= 0 {
			t.Errorf("%s = %s, ожидался ненулевой таймаут", timeout.name, timeout.got)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("собранное приложение обслуживает запросы", func(t *testing.T) {
		t.Parallel()

		dsn := pgtest.NewDSN(t)

		// Проверяется вся сборка целиком: транспорт поверх сценариев поверх
		// настоящей базы. Задача создаётся и тут же читается обратно — значит
		// слои соединены, а не просто скомпилировались.
		out := &syncBuffer{}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)

		go func() { done <- run(ctx, out, "127.0.0.1:0", dsn) }()

		addr := waitAddr(t, out)

		created := do(t, request(t, http.MethodPost, "http://"+addr+"/tasks",
			`{"title":"Купить молоко","priority":"high"}`))
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("код ответа на создание = %d, ожидался %d", created.StatusCode, http.StatusCreated)
		}

		location := created.Header.Get("Location")
		if location == "" {
			t.Fatal("в ответе на создание нет Location")
		}

		read := do(t, request(t, http.MethodGet, "http://"+addr+location, ""))
		if read.StatusCode != http.StatusOK {
			t.Fatalf("код ответа на чтение = %d, ожидался %d", read.StatusCode, http.StatusOK)
		}

		// Отмена останавливает сервер, и это штатное завершение, а не отказ.
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("run(...) вернул ошибку: %v", err)
		}
		if !strings.Contains(out.String(), "остановлен") {
			t.Errorf("сервер не сообщил об остановке:\n%s", out.String())
		}
	})

	t.Run("занятый адрес — ошибка, а не паника", func(t *testing.T) {
		t.Parallel()

		dsn := pgtest.NewDSN(t)

		busy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen(...) вернул ошибку: %v", err)
		}
		defer busy.Close()

		var out bytes.Buffer

		if err := run(t.Context(), &out, busy.Addr().String(), dsn); err == nil {
			t.Fatal("run(...) на занятом адресе вернул nil")
		}
		if out.Len() != 0 {
			t.Errorf("несостоявшийся запуск что-то напечатал:\n%s", out.String())
		}
	})

	t.Run("без строки подключения — отказ запуска", func(t *testing.T) {
		t.Parallel()

		// Подстановка хранилища в памяти при пустой переменной означала бы
		// приложение, которое «работает» ровно до первого перезапуска.
		var out bytes.Buffer

		err := run(t.Context(), &out, "127.0.0.1:0", "")
		if err == nil {
			t.Fatal("run(...) без строки подключения вернул nil")
		}
		if !strings.Contains(err.Error(), dsnEnv) {
			t.Errorf("ошибка = %q, ожидалось упоминание %s", err, dsnEnv)
		}
	})

	t.Run("задача переживает перезапуск", func(t *testing.T) {
		t.Parallel()

		// То, ради чего пункт затевался: хранилище в памяти этот подтест
		// не прошло бы никогда.
		dsn := pgtest.NewDSN(t)

		location := createTask(t, dsn)
		read := readTask(t, dsn, location)

		if read.StatusCode != http.StatusOK {
			t.Fatalf("код ответа на чтение после перезапуска = %d, ожидался %d",
				read.StatusCode, http.StatusOK)
		}
	})
}

// createTask поднимает сервер, создаёт задачу и останавливает сервер,
// возвращая путь созданной задачи.
func createTask(t *testing.T, dsn string) string {
	t.Helper()

	addr, stop := start(t, dsn)

	created := do(t, request(t, http.MethodPost, "http://"+addr+"/tasks",
		`{"title":"Купить молоко","priority":"high"}`))
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("код ответа на создание = %d, ожидался %d", created.StatusCode, http.StatusCreated)
	}

	location := created.Header.Get("Location")
	if location == "" {
		t.Fatal("в ответе на создание нет Location")
	}

	stop()

	return location
}

// readTask поднимает сервер заново на той же базе и читает задачу.
func readTask(t *testing.T, dsn, location string) *http.Response {
	t.Helper()

	addr, stop := start(t, dsn)
	defer stop()

	return do(t, request(t, http.MethodGet, "http://"+addr+location, ""))
}

// start поднимает сервер и возвращает его адрес вместе с остановкой.
//
// Остановка ждёт завершения run: без этого следующий запуск начал бы работать
// с базой, пока предыдущий ещё её закрывает.
func start(t *testing.T, dsn string) (addr string, stop func()) {
	t.Helper()

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- run(ctx, out, "127.0.0.1:0", dsn) }()

	return waitAddr(t, out), func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run(...) вернул ошибку: %v", err)
		}
	}
}

// syncBuffer — вывод, в который пишет сервер, а читает тест.
//
// Горутины разные, поэтому обычный bytes.Buffer здесь ловится -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// waitAddr дожидается напечатанного адреса и достаёт его.
//
// Порт выбирает ядро (:0), и узнать его можно только из вывода — то есть
// оттуда же, откуда его узнаёт человек, запустивший команду.
func waitAddr(t *testing.T, out *syncBuffer) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, rest, ok := strings.Cut(out.String(), "http://"); ok {
			if addr, _, ok := strings.Cut(rest, "\n"); ok {
				return strings.TrimSpace(addr)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("сервер не напечатал адрес за отведённое время:\n%s", out.String())
	return ""
}

// request готовит запрос с заголовком владельца: аутентификации нет, клиент
// называет себя сам.
func request(t *testing.T, method, url, body string) *http.Request {
	t.Helper()

	// Именно io.Reader, а не *strings.Reader: нулевой указатель в интерфейсе
	// сам по себе не nil, и NewRequest полез бы читать из него.
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("http.NewRequest(%s, %s) вернул ошибку: %v", method, url, err)
	}
	req.Header.Set("X-Owner-ID", "user-42")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// do выполняет запрос к поднятому серверу и закрывает тело ответа.
func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s вернул ошибку: %v", req.Method, req.URL, err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	return resp
}
