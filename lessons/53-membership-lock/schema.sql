-- Bootstrap only for this lesson's disposable database.
CREATE TABLE IF NOT EXISTS lesson53_members (
    tenant_id bigint NOT NULL CHECK (tenant_id > 0),
    conversation_id bigint NOT NULL CHECK (conversation_id > 0),
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    active boolean NOT NULL,
    PRIMARY KEY (tenant_id, conversation_id, actor_id)
);

CREATE TABLE IF NOT EXISTS lesson53_messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL CHECK (tenant_id > 0),
    conversation_id bigint NOT NULL CHECK (conversation_id > 0),
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 280)
);
