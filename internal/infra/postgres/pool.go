package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Сроки и пределы соединения с базой.
//
// Логика та же, по которой у HTTP-сервера закрыты все фазы соединения:
// нулевой срок означает «ждать вечно», а вечно ждущий запрос держит
// соединение, транзакцию и блокировки, взятые ею в базе. Разница лишь в том,
// что там сторона, за которой не уследить, — клиент, а здесь база.
//
// statementTimeout закрывает сам запрос: неудачный план на большой таблице
// иначе выполняется, пока кто-нибудь не заметит. idleInTransactionTimeout —
// транзакцию, которую открыли и бросили: она держит блокировки и не даёт
// вакууму убирать мёртвые версии строк. connectTimeout — установление
// соединения с недоступной базой.
const (
	statementTimeout         = 5 * time.Second
	idleInTransactionTimeout = 10 * time.Second
	connectTimeout           = 5 * time.Second

	// maxConns — потолок соединений в пуле. Postgres тратит на каждое
	// соединение процесс и память, поэтому пределы ставит клиент, а не сервер.
	maxConns = 10
	// maxConnLifetime — сколько живёт соединение до принудительной замены.
	// Без него пул переживает перезапуск базы битыми соединениями и годами
	// висит на одном узле после переключения реплики.
	maxConnLifetime = 30 * time.Minute
	// maxConnIdleTime — сколько соединение ждёт без дела, прежде чем закрыться.
	maxConnIdleTime = 5 * time.Minute
)

// parseConfig разбирает строку подключения и проставляет то, что общего у
// всякого соединения с базой: срок установления и пределы пула.
//
// Отдельно от Open, потому что нужен обоим: и пулу боевых запросов, и
// соединению мигратора.
func parseConfig(dsn string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse connection string: %w", err)
	}

	config.ConnConfig.ConnectTimeout = connectTimeout

	config.MaxConns = maxConns
	config.MaxConnLifetime = maxConnLifetime
	config.MaxConnIdleTime = maxConnIdleTime

	return config, nil
}

// limitQueries закрывает сроки самому запросу и брошенной транзакции.
//
// Проставляется только боевому пулу, но не мигратору, и это не мелочь:
// миграция законно идёт долго — индекс на большой таблице строится минутами, —
// а statement_timeout оборвал бы её на середине сообщением «canceling
// statement due to statement timeout». У боевого запроса такого права нет:
// неудачный план иначе выполняется, пока кто-нибудь не заметит.
//
// Сроки выставляются параметрами сессии, а не оборачиванием каждого запроса
// контекстом: так они действуют и на то, что запрос делает уже внутри базы,
// и на код, который забыли обернуть.
func limitQueries(config *pgxpool.Config) {
	config.ConnConfig.RuntimeParams["statement_timeout"] = milliseconds(statementTimeout)
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = milliseconds(idleInTransactionTimeout)
}

// milliseconds переводит срок в строку миллисекунд: Postgres ждёт параметры
// сессии строками.
func milliseconds(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}

// Open создаёт пул соединений и убеждается, что база отвечает.
//
// Пул создаётся без обращения к базе — pgx подключается лениво, — поэтому
// после него идёт Ping: недоступная база должна быть ошибкой запуска, а не
// молчаливой смертью на первом же запросе пользователя. Ровно по той же
// причине в композиционном корне слушатель открывается до Serve.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := parseConfig(dsn)
	if err != nil {
		return nil, err
	}
	limitQueries(config)

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres: create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping database: %w", err)
	}

	return pool, nil
}

// EnsureSchema проверяет, что схема в базе не старше той, на которую
// рассчитан код.
//
// Миграции приложение не накатывает: схему двигает выкатка отдельным шагом.
// Проверка нужна с другой стороны — чтобы отставшая база стала внятным
// отказом при старте, а не потоком ошибок про несуществующие колонки.
//
// Схема новее ожидаемой пропускается: так выглядит откат кода при накатанных
// миграциях, и запрещать его значило бы лишить себя отката.
func EnsureSchema(ctx context.Context, dsn string) error {
	expected, err := ExpectedSchemaVersion()
	if err != nil {
		return err
	}

	actual, err := SchemaVersion(ctx, dsn)
	if err != nil {
		return err
	}

	if actual < expected {
		return fmt.Errorf("postgres: check schema (database version %d, code expects %d): %w",
			actual, expected, ErrSchemaOutdated)
	}

	return nil
}
