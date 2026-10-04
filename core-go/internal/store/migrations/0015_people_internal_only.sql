-- +goose Up
-- WP17 (HAR-115, HAR-97 §4 recipient correctness): an internal-only person is an employee who must
-- never be addressed on customer-facing communication (deal desk, internal counsel). The
-- deterministic recipient eval reads this flag (contracts/schemas/deterministic_eval_input.v1.json).
ALTER TABLE people ADD COLUMN internal_only boolean NOT NULL DEFAULT false;
ALTER TABLE people ADD CONSTRAINT people_internal_only_is_employee CHECK (NOT internal_only OR kind = 'employee');

-- +goose Down
ALTER TABLE people DROP CONSTRAINT people_internal_only_is_employee;
ALTER TABLE people DROP COLUMN internal_only;
