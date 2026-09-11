import { SchemaError } from "../backend/ports";
import { operationFailureMessage } from "../operations/operation-recovery";
import {
  catalogMovedText,
  dataBlockedText,
  schemaMessage,
} from "./collection-draft";
import type { State } from "./collection-designer-state";

const operationFailed = (caught: unknown, fallback: string): string =>
  operationFailureMessage(
    caught instanceof SchemaError ? caught.details.operation?.[0] : undefined,
    fallback,
  );

export function applyFailure(caught: unknown, create: boolean): Partial<State> {
  const reason = caught instanceof SchemaError ? caught.reason : "failed";
  if (reason === "operation-conflict") {
    return {
      error:
        "This change was already sent under a different plan. Preview the change again before applying.",
      plan: null,
      confirmed: false,
    };
  }
  if (reason === "operation-failed") {
    return {
      error: operationFailed(caught, "The schema operation did not apply."),
      plan: null,
      confirmed: false,
    };
  }
  if (reason === "same-version-conflict") {
    return {
      error: create
        ? "A collection with this name likely already exists."
        : "The plan conflicts with the current state. Preview again; if it keeps failing, existing data may be blocking it.",
      plan: null,
      confirmed: false,
    };
  }
  if (reason === "catalog-moved")
    return { error: catalogMovedText, plan: null, confirmed: false };
  if (!create && reason === "data-blocked")
    return { error: schemaMessage(caught, dataBlockedText, dataBlockedText) };
  return {
    error: schemaMessage(
      caught,
      create
        ? "Unable to create the collection."
        : "Unable to apply the collection change.",
    ),
  };
}

export function deleteFailure(caught: unknown): Partial<State> {
  const reason = caught instanceof SchemaError ? caught.reason : "failed";
  if (reason === "operation-conflict") {
    return {
      deletePlan: null,
      deleteConfirmText: "",
      deleteError:
        "This deletion was already sent under a different plan. Preview the deletion again.",
    };
  }
  if (reason === "operation-failed") {
    return {
      deletePlan: null,
      deleteConfirmText: "",
      deleteError: operationFailed(
        caught,
        "The schema operation did not apply.",
      ),
    };
  }
  return {
    deletePlan: null,
    deleteConfirmText: "",
    deleteError:
      reason === "referenced"
        ? schemaMessage(
            caught,
            "Unable to delete the collection.",
            "Collection is referenced.",
          )
        : reason === "catalog-moved"
          ? catalogMovedText
          : schemaMessage(caught, "Unable to delete the collection."),
  };
}
