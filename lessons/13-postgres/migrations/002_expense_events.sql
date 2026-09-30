CREATE TABLE expense_events (
    id         bigserial PRIMARY KEY,
    expense_id bigint NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type = 'expense.created'),
    created_at timestamptz NOT NULL DEFAULT now()
);
