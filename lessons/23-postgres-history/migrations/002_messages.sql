CREATE TABLE lesson23_messages (
    id              bigserial PRIMARY KEY,
    conversation_id text NOT NULL,
    body            text NOT NULL CHECK (char_length(btrim(body)) > 0),
    created_at      timestamptz NOT NULL DEFAULT now()
);
