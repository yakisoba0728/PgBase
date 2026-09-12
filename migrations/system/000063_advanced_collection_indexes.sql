ALTER TABLE pgbase_meta.compound_indexes
    ADD COLUMN spec jsonb;

UPDATE pgbase_meta.compound_indexes AS indexes
SET spec = jsonb_build_object(
    'name', indexes.name,
    'fields', COALESCE((
        SELECT jsonb_agg(fields.name ORDER BY membership.ordinal)
        FROM pgbase_meta.compound_index_fields AS membership
        JOIN pgbase_meta.fields AS fields ON fields.id = membership.field_id
        WHERE membership.index_id = indexes.id
    ), '[]'::jsonb),
    'unique', indexes.is_unique
);

ALTER TABLE pgbase_meta.compound_indexes
    ALTER COLUMN spec SET NOT NULL,
    ADD CONSTRAINT compound_indexes_spec_shape CHECK (
        jsonb_typeof(spec) = 'object'
        AND octet_length(spec::text) <= 65536
    );
