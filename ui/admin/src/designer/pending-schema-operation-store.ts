import { parseJSON, stringifyJSON } from "../backend/json";

export type UndecidedSchemaOperation = {
  kind: "change" | "drop";
  operationId: string;
  draft: string | null;
  expiresAt: number;
};

// A server apply can run for one hour; five extra minutes allow settlement.
export const recoverabilityWindowMs = 65 * 60 * 1000;

const operationIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const legacyKey = (administratorId: string, route: string) =>
  `pgbase.schema-operation.v1.${administratorId}.${encodeURIComponent(route)}`;
const prefix = (administratorId: string, route: string) =>
  `pgbase.schema-operation.v2.${administratorId}.${encodeURIComponent(route)}.`;
const keyFor = (administratorId: string, route: string, operationId: string) =>
  `${prefix(administratorId, route)}${operationId}`;

/** Isolates the versioned, one-hour browser recovery record behind one seam. */
export class PendingSchemaOperationStore {
  load(
    administratorId: string,
    route: string,
  ): UndecidedSchemaOperation | null | false {
    try {
      const oldKey = legacyKey(administratorId, route);
      const keys = [
        oldKey,
        ...Array.from({ length: localStorage.length }, (_, index) =>
          localStorage.key(index),
        ).filter(
          (key): key is string =>
            key?.startsWith(prefix(administratorId, route)) === true,
        ),
      ];
      const candidates: UndecidedSchemaOperation[] = [];
      const now = Date.now();
      for (const key of new Set(keys)) {
        const raw = localStorage.getItem(key);
        if (raw === null) continue;
        const value = parseJSON(
          raw,
        ) as Partial<UndecidedSchemaOperation> | null;
        if (
          (value?.kind !== "change" && value?.kind !== "drop") ||
          typeof value.operationId !== "string" ||
          !operationIDPattern.test(value.operationId) ||
          typeof value.expiresAt !== "number" ||
          !Number.isSafeInteger(value.expiresAt) ||
          value.expiresAt > now + recoverabilityWindowMs ||
          (key !== oldKey &&
            key !== keyFor(administratorId, route, value.operationId))
        )
          return false;
        if (value.expiresAt <= now) localStorage.removeItem(key);
        else
          candidates.push({
            kind: value.kind,
            operationId: value.operationId,
            expiresAt: value.expiresAt,
            draft: null,
          });
      }
      candidates.sort(
        (left, right) =>
          left.expiresAt - right.expiresAt ||
          left.operationId.localeCompare(right.operationId),
      );
      return candidates[0] ?? null;
    } catch {
      return false;
    }
  }

  remember(
    administratorId: string,
    route: string,
    pending: UndecidedSchemaOperation,
  ): boolean {
    try {
      const key = keyFor(administratorId, route, pending.operationId);
      const value = stringifyJSON({
        kind: pending.kind,
        operationId: pending.operationId,
        expiresAt: pending.expiresAt,
      });
      localStorage.setItem(key, value);
      return localStorage.getItem(key) === value;
    } catch {
      return false;
    }
  }

  forget(
    administratorId: string | null,
    route: string,
    operationId: string,
  ): boolean {
    if (administratorId === null) return false;
    try {
      localStorage.removeItem(keyFor(administratorId, route, operationId));
      const oldKey = legacyKey(administratorId, route);
      const old = localStorage.getItem(oldKey);
      if (
        old !== null &&
        (parseJSON(old) as Partial<UndecidedSchemaOperation> | null)
          ?.operationId === operationId
      )
        localStorage.removeItem(oldKey);
      return true;
    } catch {
      return false;
    }
  }
}
