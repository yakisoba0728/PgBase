ALTER TABLE pgbase_meta.fields
    ADD COLUMN options_json jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(options_json) = 'object');

-- Existing installations may contain metadata with distinct API and physical
-- names after a data-preserving rename. Both remain unique within a collection.

CREATE FUNCTION pgbase_meta.rename_field_references(value jsonb, old_name text, new_name text)
RETURNS jsonb LANGUAGE sql IMMUTABLE STRICT AS $$
SELECT CASE jsonb_typeof(value)
  WHEN 'object' THEN COALESCE((SELECT jsonb_object_agg(key, CASE WHEN key='field' AND item = to_jsonb(old_name) THEN to_jsonb(new_name) WHEN key='value' THEN item ELSE pgbase_meta.rename_field_references(item,old_name,new_name) END) FROM jsonb_each(value) AS entry(key,item)),'{}'::jsonb)
  WHEN 'array' THEN COALESCE((SELECT jsonb_agg(pgbase_meta.rename_field_references(item,old_name,new_name) ORDER BY ordinal) FROM jsonb_array_elements(value) WITH ORDINALITY AS entry(item,ordinal)),'[]'::jsonb)
  ELSE value END
$$;
