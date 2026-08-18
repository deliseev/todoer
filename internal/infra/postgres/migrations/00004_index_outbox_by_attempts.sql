-- +goose Up

-- Порция набирается из наименее пробованных сообщений, а не просто из начала
-- очереди, и индекс идёт следом за запросом: без попыток в ключе минимум
-- пришлось бы искать перебором всего неотправленного — то есть ровно тогда,
-- когда неотправленного много, а база и без того не в духе.
DROP INDEX outbox_pending_idx;

CREATE INDEX outbox_pending_idx ON outbox (attempts, id) WHERE published_at IS NULL;

-- +goose Down

DROP INDEX outbox_pending_idx;

CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL;
