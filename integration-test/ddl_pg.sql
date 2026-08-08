-- PostgreSQL DDL for sqlgen integration test
CREATE TABLE IF NOT EXISTS items (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(128) NOT NULL,
    category    VARCHAR(64)  NOT NULL DEFAULT '',
    price       NUMERIC(10,2) NOT NULL DEFAULT 0,
    stock       INT          NOT NULL DEFAULT 0,
    is_active   BOOLEAN      NOT NULL DEFAULT true,
    created_at  TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMP    NOT NULL DEFAULT NOW()
);
