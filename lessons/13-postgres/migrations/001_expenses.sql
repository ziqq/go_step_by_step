CREATE TABLE expenses (
    id           bigserial PRIMARY KEY,
    description  text NOT NULL CHECK (char_length(btrim(description)) BETWEEN 1 AND 200),
    amount_cents bigint NOT NULL CHECK (amount_cents > 0),
    created_at   timestamptz NOT NULL DEFAULT now()
);
