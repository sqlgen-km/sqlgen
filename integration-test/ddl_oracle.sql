-- Oracle DDL for sqlgen integration test
CREATE TABLE items (
    id          NUMBER(19) GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        VARCHAR2(128) NOT NULL,
    category    VARCHAR2(64)  DEFAULT ' ' NOT NULL,
    price       NUMBER(10,2)  DEFAULT 0 NOT NULL,
    stock       NUMBER(10)    DEFAULT 0 NOT NULL,
    is_active   NUMBER(1)     DEFAULT 1 NOT NULL,
    created_at  TIMESTAMP     DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP     DEFAULT CURRENT_TIMESTAMP NOT NULL
);
