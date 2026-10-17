-- +goose Up
CREATE TABLE companies (
    id                  uuid        PRIMARY KEY,
    name                text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 15),
    description         text        CHECK (char_length(description) <= 3000),
    amount_of_employees integer     NOT NULL CHECK (amount_of_employees >= 0),
    registered          boolean     NOT NULL,
    type                text        NOT NULL CHECK (type IN ('Corporations', 'NonProfit', 'Cooperative', 'Sole Proprietorship')),
    version             bigint      NOT NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

-- Names are unique case-insensitively.
CREATE UNIQUE INDEX companies_name_lower_key ON companies (lower(name));

CREATE TABLE outbox (
    id           uuid        PRIMARY KEY,
    aggregate_id uuid        NOT NULL,
    event_type   text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL
);

CREATE INDEX outbox_created_at_idx ON outbox (created_at, id);

-- +goose Down
DROP TABLE outbox;
DROP TABLE companies;
