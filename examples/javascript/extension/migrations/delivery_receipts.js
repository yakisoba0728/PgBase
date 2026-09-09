const pgbase = require("pgbase");

exports.up = function up() {
  pgbase.migration.exec({
    sql: "CREATE TABLE app.n6_delivery_receipts (task_id uuid PRIMARY KEY, delivered_at timestamptz NOT NULL DEFAULT transaction_timestamp())",
  });
};

exports.down = function down() {
  pgbase.migration.exec({ sql: "DROP TABLE app.n6_delivery_receipts" });
};
