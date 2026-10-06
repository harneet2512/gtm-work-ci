-- +goose Up
-- Ask Cliff as an agent (HAR-129, owner request 2026-10-06): core keeps the conversation of a DM or thread so a
-- follow-up resolves, the task paused on a confirmation, and the trace of every answer. Slack holds none of it.
CREATE TABLE ask_turns (
    id         bigserial PRIMARY KEY,
    thread_ref text NOT NULL CHECK (thread_ref <> '' AND length(thread_ref) <= 200),
    role       text NOT NULL CHECK (role IN ('user', 'cliff')),
    body       text NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX ask_turns_thread_idx ON ask_turns (thread_ref, id);

-- At most one task is paused per conversation: the question being worked on and the action awaiting its Run.
CREATE TABLE ask_pending (
    thread_ref   text PRIMARY KEY CHECK (thread_ref <> '' AND length(thread_ref) <= 200),
    kind         text NOT NULL,
    question     text NOT NULL,
    channel_kind text NOT NULL CHECK (channel_kind IN ('dm', 'thread')),
    created_at   timestamptz NOT NULL
);

-- One row per answer: every tool called with its inputs and bounded outputs, tokens and cost (a JSON document of
-- the AskTrace schema in contracts/openapi/core.yaml).
CREATE TABLE ask_traces (
    id         text PRIMARY KEY CHECK (length(id) <= 64),
    thread_ref text NOT NULL,
    body       jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),
    created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE ask_traces;
DROP TABLE ask_pending;
DROP TABLE ask_turns;
