DO $$
DECLARE
    collection record;
BEGIN
    FOR collection IN
        SELECT name, table_name
        FROM pgbase_meta.collections
        ORDER BY name
    LOOP
        EXECUTE format(
            'CREATE INDEX IF NOT EXISTS %I ON app.%I (created_at, id)',
            'idx_page_' || substr(md5(collection.name || ':created_at:id'), 1, 16),
            collection.table_name
        );
    END LOOP;
END
$$;
