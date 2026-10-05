-- +goose Up
-- HAR-117 (WP19) and HAR-136: the outbox surfaces react to (contracts/schemas/outbox_event.v1.json) and the
-- create-only message refs that make a surface's post exactly-once (contracts/schemas/surface_message.v1.json).
-- (Numbered 0025: main's highest (0024, replay play) + 1; renumber on conflict.)
--
-- outbox_events: one row per published StrategySet and one per business-intelligence update, each written by a
-- trigger in the transaction that commits the fact, so an event exists if and only if the fact was committed (the
-- orchestrator's publish transaction and the BI writer are untouched). No foreign keys: the table is a feed, never
-- business truth, and test and replay purges must not be blocked by it. The id is allocated at insert, so it is
-- NOT a commit-order cursor: consumers list their unacknowledged events, they never resume "after id N".
CREATE TABLE outbox_events (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic                text NOT NULL CHECK (topic IN ('strategy_set.published', 'bi_update.published')),
    agent_run_id         uuid,
    strategy_set_id      uuid,
    decision_episode_id  uuid,
    bi_update_id         uuid,
    account_id           uuid NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbox_events_topic_shape CHECK (
        (topic = 'strategy_set.published' AND agent_run_id IS NOT NULL AND strategy_set_id IS NOT NULL
            AND decision_episode_id IS NOT NULL AND bi_update_id IS NULL)
        OR (topic = 'bi_update.published' AND bi_update_id IS NOT NULL AND agent_run_id IS NULL
            AND strategy_set_id IS NULL AND decision_episode_id IS NULL)),
    CONSTRAINT outbox_events_topic_run_uniq UNIQUE (topic, agent_run_id),
    CONSTRAINT outbox_events_topic_bi_uniq UNIQUE (topic, bi_update_id)
);

-- A consumer acknowledges per event; two consumers (Slack, web) never share an acknowledgement.
CREATE TABLE outbox_acks (
    consumer   text NOT NULL CHECK (consumer ~ '^[a-z][a-z0-9_-]{0,31}$'),
    event_id   bigint NOT NULL REFERENCES outbox_events (id) ON DELETE CASCADE,
    acked_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- +goose StatementBegin
CREATE FUNCTION strategy_sets_publish_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO outbox_events (topic, agent_run_id, strategy_set_id, decision_episode_id, account_id)
    VALUES ('strategy_set.published', NEW.agent_run_id, NEW.id, NEW.decision_episode_id, NEW.account_id)
    ON CONFLICT (topic, agent_run_id) DO NOTHING;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER strategy_sets_publish_event AFTER INSERT ON strategy_sets
    FOR EACH ROW EXECUTE FUNCTION strategy_sets_publish_event();

-- +goose StatementBegin
CREATE FUNCTION bi_updates_publish_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO outbox_events (topic, bi_update_id, account_id)
    VALUES ('bi_update.published', NEW.id, NEW.account_id)
    ON CONFLICT (topic, bi_update_id) DO NOTHING;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bi_updates_publish_event AFTER INSERT ON business_intelligence_updates
    FOR EACH ROW EXECUTE FUNCTION bi_updates_publish_event();

-- surface_messages: where a surface put a message, so a message is posted exactly once per subject. The subject is
-- the DecisionEpisode for the strategy chooser (Message 2) and the judgment (Message 3), and the
-- business-intelligence update for Message 1 (an update exists before its episode does). One row per
-- (subject, surface, kind), created by a reservation before the post; `ts` is written once, after the post
-- (a trigger refuses to change it). A reservation without a ts means a post may or may not have happened: the
-- surface reconciles it against the channel's history (Slack message metadata) before posting again. No foreign
-- keys, like the outbox.
CREATE TABLE surface_messages (
    subject_id   uuid NOT NULL,
    surface      text NOT NULL CHECK (surface ~ '^[a-z][a-z0-9_-]{0,31}$'),
    kind         text NOT NULL CHECK (kind IN ('bi', 'chooser', 'judgment')),
    channel      text NOT NULL CHECK (length(channel) BETWEEN 1 AND 64),
    ts           text CHECK (ts IS NULL OR ts ~ '^[0-9]+\.[0-9]+$'),
    reserved_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_id, surface, kind)
);

-- +goose StatementBegin
CREATE FUNCTION surface_messages_ts_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.subject_id <> OLD.subject_id OR NEW.surface <> OLD.surface OR NEW.kind <> OLD.kind
       OR NEW.channel <> OLD.channel OR NEW.reserved_at <> OLD.reserved_at THEN
        RAISE EXCEPTION 'surface_messages: only ts may be written' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.ts IS NOT NULL AND NEW.ts IS DISTINCT FROM OLD.ts THEN
        RAISE EXCEPTION 'surface_messages: ts is written once' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER surface_messages_ts_once BEFORE UPDATE ON surface_messages
    FOR EACH ROW EXECUTE FUNCTION surface_messages_ts_once();

-- +goose Down
DROP TRIGGER surface_messages_ts_once ON surface_messages;
DROP FUNCTION surface_messages_ts_once();
DROP TABLE surface_messages;
DROP TRIGGER bi_updates_publish_event ON business_intelligence_updates;
DROP FUNCTION bi_updates_publish_event();
DROP TRIGGER strategy_sets_publish_event ON strategy_sets;
DROP FUNCTION strategy_sets_publish_event();
DROP TABLE outbox_acks;
DROP TABLE outbox_events;
