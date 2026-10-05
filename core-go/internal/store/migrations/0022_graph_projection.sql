-- +goose Up
-- Neo4j context-graph projection (HAR-96 Architecture update, HAR-129 1B): the transactional outbox,
-- the per-account projection checkpoint and the recorded graph diff of every projection.
-- Postgres stays authoritative; none of these tables holds business truth, they only drive and
-- describe the rebuildable projection.

-- Outbox. Like recompute_jobs: at most one PENDING and one IN-FLIGHT job per account. An enqueue merges
-- into the pending job (activity ids unioned, the first enqueued_at kept, so lag is measured from the
-- oldest unprojected change). A job is kept after completion (completed_at) because its diff and its
-- enqueue-to-complete latency are queryable. An expired lease is reclaimable.
CREATE TABLE graph_projection_jobs (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id        uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    enqueued_at       timestamptz NOT NULL DEFAULT now(),
    activity_ids      uuid[] NOT NULL DEFAULT '{}',
    reasons           text[] NOT NULL DEFAULT '{}',
    claimed_at        timestamptz,
    claimed_by        text,
    lease_expires_at  timestamptz,
    completed_at      timestamptz,
    attempts          integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error        text,
    CONSTRAINT graph_projection_jobs_claim_has_lease CHECK ((claimed_at IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT graph_projection_jobs_complete_is_claimed CHECK (completed_at IS NULL OR claimed_at IS NOT NULL)
);
CREATE UNIQUE INDEX graph_projection_jobs_pending_uniq ON graph_projection_jobs (account_id) WHERE claimed_at IS NULL;
CREATE UNIQUE INDEX graph_projection_jobs_inflight_uniq ON graph_projection_jobs (account_id)
    WHERE claimed_at IS NOT NULL AND completed_at IS NULL;
CREATE INDEX graph_projection_jobs_due_idx ON graph_projection_jobs (enqueued_at) WHERE claimed_at IS NULL;
CREATE INDEX graph_projection_jobs_done_idx ON graph_projection_jobs (account_id, completed_at DESC) WHERE completed_at IS NOT NULL;
CREATE INDEX graph_projection_jobs_activity_ids_gin ON graph_projection_jobs USING gin (activity_ids);

-- What the graph held for an account after its latest completed projection: counts and one hash over every
-- node and edge hash. Rebuild and drift compare against it; it is a cache of the graph, never an input.
CREATE TABLE graph_projection_checkpoints (
    account_id     uuid PRIMARY KEY REFERENCES accounts (id) ON DELETE RESTRICT,
    last_job_id    bigint REFERENCES graph_projection_jobs (id) ON DELETE SET NULL,
    projected_at   timestamptz NOT NULL,
    node_count     integer NOT NULL CHECK (node_count >= 0),
    edge_count     integer NOT NULL CHECK (edge_count >= 0),
    snapshot_hash  text NOT NULL CHECK (snapshot_hash ~ '^[0-9a-f]{64}$'),
    skipped        jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(skipped) = 'object')
);

-- The exact nodes and edges one projection added, changed or removed (HAR-129 1B "show exact graph diff").
-- source_event_ids lets "what did event N change in the graph" be answered from Postgres.
CREATE TABLE graph_projection_diffs (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id            bigint NOT NULL UNIQUE REFERENCES graph_projection_jobs (id) ON DELETE CASCADE,
    account_id        uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    activity_ids      uuid[] NOT NULL DEFAULT '{}',
    source_event_ids  uuid[] NOT NULL DEFAULT '{}',
    summary           jsonb NOT NULL CHECK (jsonb_typeof(summary) = 'object'),
    changes           jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(changes) = 'array'),
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX graph_projection_diffs_account_idx ON graph_projection_diffs (account_id, id DESC);
CREATE INDEX graph_projection_diffs_events_gin ON graph_projection_diffs USING gin (source_event_ids);

-- The draft agent's ctx tools gain graph_neighborhood (the bounded Neo4j neighborhood of the run's account).
ALTER TABLE context_access_log DROP CONSTRAINT context_access_log_tool_check;
ALTER TABLE context_access_log ADD CONSTRAINT context_access_log_tool_check CHECK (tool IN (
    'state', 'recent_diffs', 'evidence', 'activities', 'people', 'commitments', 'graph_neighborhood'));

-- Outbox coverage for what the graph snapshot reads but ingest and recompute do not write: decision episodes,
-- knowledge and its evidence, and the knowledge a run used. These writers (HAR-97) run in their own
-- transactions, so the enqueue is a trigger: the job commits with the write, whoever makes it. Knowledge is
-- global, so a knowledge write enqueues every account whose episodes touch it. (Activities, claims, signals,
-- relationships and state are enqueued by the ingest and recompute hooks, ctxgraph.IngestHook / WithProjection.)
-- +goose StatementBegin
CREATE FUNCTION graph_enqueue_account(p_account uuid, p_reason text) RETURNS void LANGUAGE sql AS $$
    INSERT INTO graph_projection_jobs (account_id, reasons) VALUES (p_account, ARRAY[p_reason])
    ON CONFLICT (account_id) WHERE claimed_at IS NULL DO UPDATE
       SET reasons = (SELECT array_agg(DISTINCT x ORDER BY x) FROM unnest(graph_projection_jobs.reasons || EXCLUDED.reasons) AS u(x))
$$;

CREATE FUNCTION graph_enqueue_knowledge_accounts(p_knowledge uuid) RETURNS void LANGUAGE sql AS $$
    SELECT graph_enqueue_account(a, 'knowledge') FROM (
        SELECT de.account_id AS a FROM knowledge_evidence ke JOIN decision_episodes de ON de.id = ke.ref_id
         WHERE ke.knowledge_id = p_knowledge AND ke.kind = 'decision_episode'
        UNION
        SELECT ar.account_id FROM agent_runs ar WHERE ar.knowledge_refs_used ? p_knowledge::text) linked
$$;

CREATE FUNCTION graph_trg_episode() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM graph_enqueue_account(NEW.account_id, 'episode');
    RETURN NULL;
END $$;

CREATE FUNCTION graph_trg_knowledge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM graph_enqueue_knowledge_accounts(NEW.id);
    RETURN NULL;
END $$;

CREATE FUNCTION graph_trg_knowledge_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM graph_enqueue_knowledge_accounts(NEW.knowledge_id);
    RETURN NULL;
END $$;

CREATE FUNCTION graph_trg_run_knowledge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF jsonb_array_length(NEW.knowledge_refs_used) > 0 THEN
        PERFORM graph_enqueue_account(NEW.account_id, 'knowledge');
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER graph_episode AFTER INSERT OR UPDATE ON decision_episodes FOR EACH ROW EXECUTE FUNCTION graph_trg_episode();
CREATE TRIGGER graph_knowledge AFTER UPDATE ON knowledge FOR EACH ROW EXECUTE FUNCTION graph_trg_knowledge();
CREATE TRIGGER graph_knowledge_evidence AFTER INSERT OR UPDATE ON knowledge_evidence FOR EACH ROW EXECUTE FUNCTION graph_trg_knowledge_evidence();
CREATE TRIGGER graph_run_knowledge AFTER INSERT OR UPDATE OF knowledge_refs_used ON agent_runs FOR EACH ROW EXECUTE FUNCTION graph_trg_run_knowledge();

-- +goose Down
DROP TRIGGER graph_run_knowledge ON agent_runs;
DROP TRIGGER graph_knowledge_evidence ON knowledge_evidence;
DROP TRIGGER graph_knowledge ON knowledge;
DROP TRIGGER graph_episode ON decision_episodes;
DROP FUNCTION graph_trg_run_knowledge();
DROP FUNCTION graph_trg_knowledge_evidence();
DROP FUNCTION graph_trg_knowledge();
DROP FUNCTION graph_trg_episode();
DROP FUNCTION graph_enqueue_knowledge_accounts(uuid);
DROP FUNCTION graph_enqueue_account(uuid, text);
DELETE FROM context_access_log WHERE tool = 'graph_neighborhood';
ALTER TABLE context_access_log DROP CONSTRAINT context_access_log_tool_check;
ALTER TABLE context_access_log ADD CONSTRAINT context_access_log_tool_check CHECK (tool IN (
    'state', 'recent_diffs', 'evidence', 'activities', 'people', 'commitments'));
DROP TABLE graph_projection_diffs;
DROP TABLE graph_projection_checkpoints;
DROP TABLE graph_projection_jobs;
