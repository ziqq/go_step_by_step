-- Bootstrap only for this lesson's disposable database, not a migration runner.
-- Rows are immutable and retained for the full supported replay lifetime.
CREATE TABLE IF NOT EXISTS lesson52_sends (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL CHECK (tenant_id > 0),
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    command_key text COLLATE "C" NOT NULL CHECK (octet_length(command_key) BETWEEN 1 AND 64),
    conversation_id bigint NOT NULL CHECK (conversation_id > 0),
    body text COLLATE "C" NOT NULL CHECK (char_length(body) BETWEEN 1 AND 280),
    UNIQUE (tenant_id, actor_id, command_key)
);
