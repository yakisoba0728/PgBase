import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import { translate, type MessageKey } from "../i18n";
import { viewSqlProblem } from "./SchemaRows";

const t = (key: MessageKey, vars?: Record<string, string | number>) => translate("en", key, vars);

describe("viewSqlProblem", () => {
  it("shows PostgreSQL's message and the position inside the query", () => {
    const error = new ApiError(422, "invalid_view_sql", "SQL view is invalid", {
      "view.sql": ["invalid_sql"],
      "view.sql.message": ['relation "posts" does not exist'],
      "view.sql.position": ["16"],
      "view.sql.sqlState": ["42P01"],
    });
    expect(viewSqlProblem(t, error)).toBe('PostgreSQL rejected the query. — relation "posts" does not exist — at character 16');
  });

  it("names the rejected column or relation for server-side checks", () => {
    const error = new ApiError(422, "invalid_view_sql", "", { "view.sql": ["relation_not_collection"], "view.sql.relation": ["pgbase_meta.admins"] });
    expect(viewSqlProblem(t, error)).toBe("A view may read only collections (app.<name>). — pgbase_meta.admins");
  });

  it("ignores other errors", () => {
    expect(viewSqlProblem(t, new ApiError(409, "schema_conflict", ""))).toBeUndefined();
    expect(viewSqlProblem(t, null)).toBeUndefined();
  });
});
