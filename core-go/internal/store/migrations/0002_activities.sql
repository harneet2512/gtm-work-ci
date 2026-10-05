-- +goose Up
-- Raw source events (retained verbatim) and the normalized activity graph.

CREATE TABLE source_events (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_system      text NOT NULL CHECK (source_system IN (
        'crm', 'email', 'calendar', 'call', 'slack', 'sales_engagement', 'marketing',
        'product', 'support', 'docs', 'enrichment', 'agent', 'human', 'ghost.clock')),
    source_object_id   text NOT NULL CHECK (length(source_object_id) BETWEEN 1 AND 512),
    source_event_key   text NOT NULL CHECK (length(source_event_key) BETWEEN 1 AND 256),
    -- Derived lookup key: hex sha256 of the length-prefixed triple (see core-go ingest).
    -- The real idempotency guard is the UNIQUE triple below.
    idempotency_key    text NOT NULL UNIQUE CHECK (idempotency_key ~ '^[0-9a-f]{64}$'),
    connector          text,
    connector_version  text,
    occurred_at        timestamptz,
    received_at        timestamptz NOT NULL DEFAULT now(),
    payload            jsonb NOT NULL,
    -- Duplicate deliveries bump this instead of creating rows.
    delivery_count     integer NOT NULL DEFAULT 1 CHECK (delivery_count >= 1),
    last_delivered_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT source_events_identity_uniq UNIQUE (source_system, source_object_id, source_event_key)
);

CREATE TABLE activities (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 1:1 with the source event; idempotency is inherited from source_events.
    source_event_id        uuid NOT NULL UNIQUE REFERENCES source_events (id) ON DELETE RESTRICT,
    activity_type          text NOT NULL CHECK (activity_type IN (
        'EmailSent', 'EmailReceived', 'EmailReply',
        'CallStarted', 'CallEnded', 'TranscriptReady',
        'MeetingScheduled', 'MeetingAccepted', 'MeetingDeclined', 'MeetingCompleted', 'MeetingParticipantAdded',
        'SlackMessage', 'SlackDecision',
        'CRMFieldChanged', 'CRMNoteAdded', 'OpportunityStageChanged', 'ContactAdded', 'StakeholderRoleChanged',
        'MarketingSignal', 'WebsiteIntent', 'ProductUsageChanged',
        'SupportTicketOpened', 'SupportTicketResolved', 'DocumentShared', 'EnrichmentUpdated',
        'AgentActionProposed', 'AgentActionExecuted',
        'HumanApproved', 'HumanEdited', 'HumanRejected',
        'CustomerReplied', 'CustomerWentSilent', 'BusinessOutcomeChanged')),
    source_system          text NOT NULL CHECK (source_system IN (
        'crm', 'email', 'calendar', 'call', 'slack', 'sales_engagement', 'marketing',
        'product', 'support', 'docs', 'enrichment', 'agent', 'human', 'ghost.clock')),
    source_object_id       text NOT NULL,
    occurred_at            timestamptz NOT NULL,
    ingested_at            timestamptz NOT NULL DEFAULT now(),
    account_id             uuid REFERENCES accounts (id) ON DELETE RESTRICT,
    opportunity_id         uuid,
    account_hint           text,
    opportunity_hint       text,
    summary                text,
    body_text              text,  -- normalized plain text used for extraction and evidence quotes
    permissions            jsonb NOT NULL DEFAULT '{"visibility": "org"}'::jsonb,
    provenance             jsonb NOT NULL,
    caused_by_activity_id  uuid REFERENCES activities (id) ON DELETE SET NULL,
    correlation_id         uuid,
    CONSTRAINT activities_opportunity_needs_account CHECK (opportunity_id IS NULL OR account_id IS NOT NULL),
    CONSTRAINT activities_opportunity_same_account FOREIGN KEY (opportunity_id, account_id)
        REFERENCES opportunities (id, account_id) ON DELETE RESTRICT,
    CONSTRAINT activities_not_caused_by_self CHECK (caused_by_activity_id IS NULL OR caused_by_activity_id <> id)
);
-- Timeline with stable cursor pagination.
CREATE INDEX activities_account_time_idx ON activities (account_id, occurred_at DESC, id DESC);
CREATE INDEX activities_opportunity_time_idx ON activities (opportunity_id, occurred_at DESC, id DESC);
CREATE INDEX activities_correlation_idx ON activities (correlation_id) WHERE correlation_id IS NOT NULL;
CREATE INDEX activities_caused_by_idx ON activities (caused_by_activity_id) WHERE caused_by_activity_id IS NOT NULL;
CREATE INDEX activities_unresolved_idx ON activities (ingested_at) WHERE account_id IS NULL;

CREATE TABLE activity_participants (
    activity_id   uuid NOT NULL REFERENCES activities (id) ON DELETE CASCADE,
    raw_identity  text NOT NULL CHECK (length(raw_identity) BETWEEN 1 AND 512),
    role          text NOT NULL CHECK (role IN ('actor', 'from', 'to', 'cc', 'bcc', 'attendee', 'organizer', 'speaker', 'mentioned', 'owner')),
    display_name  text,
    person_id     uuid REFERENCES people (id) ON DELETE SET NULL,
    PRIMARY KEY (activity_id, raw_identity, role)
);
CREATE INDEX activity_participants_person_idx ON activity_participants (person_id);

ALTER TABLE entity_source_mappings
    ADD CONSTRAINT entity_source_mappings_evidence_fk
    FOREIGN KEY (evidence_activity_id) REFERENCES activities (id) ON DELETE SET NULL;
CREATE INDEX entity_source_mappings_evidence_idx ON entity_source_mappings (evidence_activity_id);

ALTER TABLE relationships
    ADD CONSTRAINT relationships_source_activity_fk
    FOREIGN KEY (source_activity_id) REFERENCES activities (id) ON DELETE SET NULL;
CREATE INDEX relationships_source_activity_idx ON relationships (source_activity_id);

-- +goose Down
DROP INDEX relationships_source_activity_idx;
ALTER TABLE relationships DROP CONSTRAINT relationships_source_activity_fk;
DROP INDEX entity_source_mappings_evidence_idx;
ALTER TABLE entity_source_mappings DROP CONSTRAINT entity_source_mappings_evidence_fk;
DROP TABLE activity_participants;
DROP TABLE activities;
DROP TABLE source_events;
