-- +goose Up
-- json keeps the payload byte for byte as the service serialized it; jsonb reorders keys.
ALTER TABLE outbox ALTER COLUMN payload TYPE json;

-- +goose Down
ALTER TABLE outbox ALTER COLUMN payload TYPE jsonb;
