// Команда todoer — композиционный корень приложения.
//
// Здесь и только здесь слои встречаются: инфраструктура подставляется в порты
// сценариев, транспорт зовёт сценарии, а те работают с доменом. Ни один пакет
// ниже про эту сборку не знает и знать не должен.
//
// Команда поднимает HTTP-сервер и работает, пока её не остановят: отменой
// контекста или сигналом. Задачи хранятся в Postgres, поэтому остановка их
// не стирает; схему двигает отдельная команда todoer-migrate.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/deliseev/todoer/internal/app"
	"github.com/deliseev/todoer/internal/infra"
	"github.com/deliseev/todoer/internal/infra/postgres"
	"github.com/deliseev/todoer/internal/transport/httpapi"
)

const (
	// addrEnv — переменная окружения с адресом прослушивания, defaultAddr —
	// адрес, если её нет. Порт в коде не зашит: занятый порт на машине
	// разработчика не повод править исходники.
	addrEnv     = "TODOER_ADDR"
	defaultAddr = ":8080"

	// dsnEnv — переменная окружения со строкой подключения к базе. Умолчания
	// у неё нет и быть не должно: адрес базы — это то, чего нельзя угадать,
	// а угаданный неверно он молча уведёт запись не туда.
	dsnEnv = "TODOER_DATABASE_URL"

	// Таймауты сервера. Нулевой таймаут у http.Server значит «ждать вечно»,
	// а вечно ждущее соединение — самый дешёвый отказ в обслуживании из
	// возможных: он не стоит атакующему ничего. Поэтому закрыты все фазы,
	// а не одни заголовки.
	//
	// readHeaderTimeout — молчащее соединение, не приславшее заголовков;
	// readTimeout — весь запрос целиком, то есть и тело, сочащееся по байту;
	// writeTimeout — работа хендлера и отправка ответа, включая клиента,
	// который не читает; idleTimeout — сон между запросами в keep-alive.
	//
	// Числа с запасом: тело ограничено 64 КиБ, а хендлеры работают с памятью,
	// так что законный запрос не приближается к этим границам.
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	// shutdownTimeout — сколько ждать текущие запросы при остановке. Дальше
	// сервер закрывается, не дожидаясь их: висящий бесконечно процесс хуже
	// оборванного ответа, и его всё равно добьёт супервизор.
	shutdownTimeout = 10 * time.Second
)

func main() {
	// Сигнал приходит отменой контекста, поэтому остановка по Ctrl+C и
	// остановка по отмене — один и тот же путь, а не два разных.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	restoreSignalsOnCancel(ctx, stop)

	if err := run(ctx, os.Stdout, listenAddr(), databaseURL()); err != nil {
		fmt.Fprintln(os.Stderr, "todoer:", err)
		os.Exit(1)
	}
}

// restoreSignalsOnCancel возвращает сигналам обычное поведение, как только
// контекст отменён.
//
// Первый сигнал запускает остановку, а она длится до shutdownTimeout. Всё это
// время обработчик, поставленный NotifyContext, продолжает ловить сигналы —
// и второй Ctrl+C оператора, которому надоело ждать, не делает ничего:
// контекст уже отменён. Сняв обработчик сразу после отмены, мы возвращаем
// второму сигналу его обычное действие — убить процесс.
func restoreSignalsOnCancel(ctx context.Context, restore func()) {
	go func() {
		<-ctx.Done()
		restore()
	}()
}

// listenAddr выбирает адрес прослушивания: из окружения, иначе умолчание.
//
// Пустая переменная равна отсутствующей: пробелы — не адрес.
func listenAddr() string {
	if addr := strings.TrimSpace(os.Getenv(addrEnv)); addr != "" {
		return addr
	}
	return defaultAddr
}

// databaseURL читает строку подключения из окружения.
// Пустая переменная равна отсутствующей: пробелы — не адрес.
func databaseURL() string {
	return strings.TrimSpace(os.Getenv(dsnEnv))
}

// newTaskService собирает сервис на боевых реализациях портов.
//
// Публикатор заглушечный: шины событий ещё нет, а nil вместо него запрещён
// конструктором — это тихая потеря событий.
func newTaskService(repo app.Repository) (*app.TaskService, error) {
	return app.NewTaskService(
		repo,
		infra.NopPublisher{},
		infra.SystemClock{},
	)
}

// run поднимает сервер на addr и обслуживает запросы, пока не отменят ctx.
//
// Отделён от main, чтобы его мог водить тест: адрес и строка подключения
// приезжают параметрами, а не из окружения, вывод — в out, а не в os.Stdout.
// Отмена контекста — штатное завершение, поэтому run возвращает nil.
func run(ctx context.Context, out io.Writer, addr, dsn string) error {
	if dsn == "" {
		return fmt.Errorf("%s не задана", dsnEnv)
	}

	// База открывается прежде всего остального, и по той же причине, по
	// которой слушатель открывается до Serve: недоступная база должна быть
	// ошибкой запуска, а не потоком пятисоток по каждому запросу.
	pool, err := postgres.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Миграции команда не накатывает — их двигает выкатка, — но работать на
	// отставшей схеме нельзя: код обращался бы к колонкам, которых ещё нет.
	if err := postgres.EnsureSchema(ctx, dsn); err != nil {
		return err
	}

	service, err := newTaskService(postgres.NewTaskRepository(pool))
	if err != nil {
		return fmt.Errorf("build task service: %w", err)
	}

	handler, err := httpapi.NewHandler(service)
	if err != nil {
		return fmt.Errorf("build http handler: %w", err)
	}

	// Слушатель открывается до Serve, чтобы занятый порт стал ошибкой запуска,
	// а не молчаливой смертью в горутине. Он же сообщает настоящий адрес:
	// с портом 0 его выбирает ядро.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	server := newServer(handler.Routes())

	fmt.Fprintf(out, "todoer слушает http://%s\n", listener.Addr())

	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	select {
	case err := <-served:
		// Сервер лёг сам, никто его не просил.
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		return shutdown(ctx, out, server)
	}
}

// newServer собирает сервер с закрытыми таймаутами всех фаз.
//
// Вынесен отдельно, чтобы тест мог убедиться, что ни один из них не остался
// нулевым: пропажу таймаута иначе видно только под нагрузкой, когда чинить
// уже поздно.
func newServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// shutdown останавливает сервер, дав договорить текущим запросам.
//
// Контекст остановки строится через WithoutCancel: ctx к этому моменту уже
// отменён, и передать его как есть значило бы оборвать те самые запросы,
// которые мы собрались дождаться.
func shutdown(ctx context.Context, out io.Writer, server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	// Serve после Shutdown возвращает ErrServerClosed — это не отказ, а отчёт
	// о том, что закрылись как просили.
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}

	fmt.Fprintln(out, "todoer остановлен")
	return nil
}
