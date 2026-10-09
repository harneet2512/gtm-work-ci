-- +goose Up
-- HAR-119 / HAR-139: generated knowledge keys must skip keys already in use.
--
-- knowledge.key is a human-facing K<n> label that people may also assign explicitly. The default
-- ('K' || nextval(knowledge_key_seq)) assumed it owned the whole K<n> space, so a human-created K4
-- made the next generated key (the learning seeds insert without a key) collide on knowledge_key_key
-- and fail the send / corrected-verdict write with an internal error. knowledge_next_key() draws from
-- the same sequence but advances past any key that is taken.
-- +goose StatementBegin
CREATE FUNCTION knowledge_next_key() RETURNS text LANGUAGE plpgsql AS $$
DECLARE
    candidate text;
BEGIN
    LOOP
        candidate := 'K' || nextval('knowledge_key_seq');
        EXIT WHEN NOT EXISTS (SELECT 1 FROM knowledge WHERE key = candidate);
    END LOOP;
    RETURN candidate;
END;
$$;
-- +goose StatementEnd
ALTER TABLE knowledge ALTER COLUMN key SET DEFAULT knowledge_next_key();

-- +goose Down
ALTER TABLE knowledge ALTER COLUMN key SET DEFAULT ('K' || nextval('knowledge_key_seq'));
DROP FUNCTION knowledge_next_key();
