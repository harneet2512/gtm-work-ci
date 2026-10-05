-- +goose Up
-- Extend the edge vocabulary with economic_buyer_for (HAR-96 §6: economic buyer known/unknown)
-- and supports (an internal SE/manager supporting a deal). Must equal common.v1.json#/$defs/relType.
ALTER TABLE relationships DROP CONSTRAINT relationships_rel_type_check;
ALTER TABLE relationships ADD CONSTRAINT relationships_rel_type_check CHECK (rel_type IN (
    'works_at', 'participated_in', 'champion_for', 'economic_buyer_for', 'influences', 'owns', 'supports',
    'belongs_to', 'about', 'involves', 'shared_with', 'delegated_to', 'reports_to', 'evaluates', 'blocks'));

-- +goose Down
ALTER TABLE relationships DROP CONSTRAINT relationships_rel_type_check;
ALTER TABLE relationships ADD CONSTRAINT relationships_rel_type_check CHECK (rel_type IN (
    'works_at', 'participated_in', 'champion_for', 'influences', 'owns',
    'belongs_to', 'about', 'involves', 'shared_with', 'delegated_to',
    'reports_to', 'evaluates', 'blocks'));
