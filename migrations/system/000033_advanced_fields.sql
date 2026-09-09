ALTER TABLE pgbase_meta.fields
    DROP CONSTRAINT fields_field_type_check,
    ADD CONSTRAINT fields_field_type_check CHECK (
        field_type IN (
            'boolean','integer','bigint','numeric','text','editor','geoPoint',
            'autodate','password','timestamptz','uuid','jsonb','relation','file'
        )
    );
