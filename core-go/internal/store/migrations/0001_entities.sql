-- +goose Up
-- Canonical commercial entities and the identity-resolution layer.
-- The graph is relational: nodes are entity tables, edges live in `relationships`.
-- Accounts are never hard-deleted (audit trail); child FKs use RESTRICT.

CREATE TABLE accounts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 300),
    domain      text UNIQUE CHECK (domain = lower(domain)),
    archived_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE people (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind           text NOT NULL CHECK (kind IN ('contact', 'employee')),
    display_name   text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 300),
    primary_email  text UNIQUE CHECK (primary_email = lower(primary_email)),
    title          text,
    account_id     uuid REFERENCES accounts (id) ON DELETE RESTRICT,
    merged_into    uuid REFERENCES people (id) ON DELETE RESTRICT,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT people_employee_has_no_account CHECK (kind = 'contact' OR account_id IS NULL),
    CONSTRAINT people_not_merged_into_self CHECK (merged_into IS NULL OR merged_into <> id)
);
CREATE INDEX people_account_idx ON people (account_id);

CREATE TABLE opportunities (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    name             text NOT NULL,
    motion           text NOT NULL CHECK (motion IN ('new_business', 'expansion', 'renewal')),
    owner_person_id  uuid REFERENCES people (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- Target for composite FKs that guarantee an opportunity belongs to the stated account.
    CONSTRAINT opportunities_id_account_uniq UNIQUE (id, account_id)
);
CREATE INDEX opportunities_account_idx ON opportunities (account_id);
CREATE INDEX opportunities_owner_idx ON opportunities (owner_person_id);

CREATE TABLE products (
    id    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name  text NOT NULL UNIQUE
);

-- Every source identity resolved onto a canonical entity, and why. A remap closes the old
-- row (valid_to) and inserts a new one, so the history of "why" is kept.
-- e.g. ('crm','contact:817') + ('email','priya@acme.com') + ('call','C19:speaker:02') -> person P17
CREATE TABLE entity_source_mappings (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type           text NOT NULL CHECK (entity_type IN ('account', 'person', 'opportunity', 'product')),
    entity_id             uuid NOT NULL,
    source_system         text NOT NULL,
    source_key            text NOT NULL CHECK (length(source_key) BETWEEN 1 AND 512),
    confidence            numeric(4, 3) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    method                text NOT NULL CHECK (method IN ('exact', 'rule', 'llm', 'human', 'seed')),
    evidence_activity_id  uuid,  -- FK added in 0002 once activities exists
    valid_from            timestamptz NOT NULL DEFAULT now(),
    valid_to              timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entity_source_mappings_interval CHECK (valid_to IS NULL OR valid_to > valid_from)
);
-- Exactly one current mapping per source identity.
CREATE UNIQUE INDEX entity_source_mappings_current_uniq
    ON entity_source_mappings (source_system, source_key) WHERE valid_to IS NULL;
CREATE INDEX entity_source_mappings_entity_idx ON entity_source_mappings (entity_type, entity_id);

-- Typed edges. Edges carry standing/confidence and are closed with valid_to rather than
-- deleted, so relationship history is kept.
CREATE TABLE relationships (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    src_type            text NOT NULL CHECK (src_type IN ('account', 'person', 'opportunity', 'product', 'activity', 'document')),
    src_id              uuid NOT NULL,
    rel_type            text NOT NULL CHECK (rel_type IN (
                            'works_at', 'participated_in', 'champion_for', 'influences', 'owns',
                            'belongs_to', 'about', 'involves', 'shared_with', 'delegated_to',
                            'reports_to', 'evaluates', 'blocks')),
    dst_type            text NOT NULL CHECK (dst_type IN ('account', 'person', 'opportunity', 'product', 'activity', 'document')),
    dst_id              uuid NOT NULL,
    standing            text NOT NULL CHECK (standing IN ('human_approved', 'crm_explicit', 'first_party_ai', 'third_party')),
    confidence          numeric(4, 3) NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    source_activity_id  uuid,  -- FK added in 0002
    valid_from          timestamptz NOT NULL,
    valid_to            timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT relationships_interval CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT relationships_no_self_edge CHECK (NOT (src_type = dst_type AND src_id = dst_id))
);
CREATE INDEX relationships_src_idx ON relationships (src_type, src_id, rel_type) WHERE valid_to IS NULL;
CREATE INDEX relationships_dst_idx ON relationships (dst_type, dst_id, rel_type) WHERE valid_to IS NULL;
CREATE UNIQUE INDEX relationships_open_uniq
    ON relationships (src_type, src_id, rel_type, dst_type, dst_id, standing) WHERE valid_to IS NULL;

-- +goose Down
DROP TABLE relationships;
DROP TABLE entity_source_mappings;
DROP TABLE products;
DROP TABLE opportunities;
DROP TABLE people;
DROP TABLE accounts;
