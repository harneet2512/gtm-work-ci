-- +goose Up
-- Standing wins, conflicts are surfaced (decision 2026-10-02, HAR-96 §18): when newer first-party
-- evidence contradicts a CRM-explicit field, the state keeps the CRM value and emits
-- field_contradicted so the rep can confirm (a human-approved claim then outranks CRM).
ALTER TABLE signals DROP CONSTRAINT signals_signal_type_check;
ALTER TABLE signals ADD CONSTRAINT signals_signal_type_check CHECK (signal_type IN (
    'new_stakeholder_entered', 'champion_weakened', 'champion_reactivated', 'champion_delegated',
    'security_blocker_appeared', 'blocker_resolved', 'pricing_interest', 'expansion_interest',
    'stakeholder_gap', 'next_meeting_missing', 'commitment_overdue', 'customer_replied',
    'customer_went_silent', 'meeting_accepted', 'support_risk_spike', 'product_usage_increased',
    'stage_regressed', 'stage_advanced', 'field_contradicted'));

-- +goose Down
ALTER TABLE signals DROP CONSTRAINT signals_signal_type_check;
ALTER TABLE signals ADD CONSTRAINT signals_signal_type_check CHECK (signal_type IN (
    'new_stakeholder_entered', 'champion_weakened', 'champion_reactivated', 'champion_delegated',
    'security_blocker_appeared', 'blocker_resolved', 'pricing_interest', 'expansion_interest',
    'stakeholder_gap', 'next_meeting_missing', 'commitment_overdue', 'customer_replied',
    'customer_went_silent', 'meeting_accepted', 'support_risk_spike', 'product_usage_increased',
    'stage_regressed', 'stage_advanced'));
