CREATE TABLE IF NOT EXISTS lesson27_messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id text NOT NULL CHECK (length(conversation_id) BETWEEN 1 AND 80),
    body text NOT NULL CHECK (length(body) BETWEEN 1 AND 280),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS lesson27_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id bigint NOT NULL UNIQUE REFERENCES lesson27_messages (id),
    event_type text NOT NULL CHECK (event_type = 'message.created'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);
