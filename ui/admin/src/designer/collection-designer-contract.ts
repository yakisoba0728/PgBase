import type {
  CollectionDefinition,
  CollectionType,
  CompoundIndex,
  RecordIDPolicy,
  ViewDefinition,
} from "../backend/ports";
import type { OperationRecovery } from "../operations/operation-recovery";
import type {
  DraftEdit,
  DraftField,
  RelationTargetOption,
} from "./collection-draft";
import type {
  CollectionPreview,
  DeleteTarget,
  State,
} from "./collection-designer-state";

export interface CollectionDesigner {
  phase: State["phase"];
  create: boolean;
  name: string;
  tableName: string;
  fields: DraftField[];
  relationTargets: RelationTargetOption[];
  relationTargetsLoading: boolean;
  relationTargetsFailed: boolean;
  isNewField(name: string): boolean;
  collectionType: CollectionType;
  advanced?: import("../backend/ports").CollectionAdvancedOptions;
  setAdvanced(value: import("../backend/ports").CollectionAdvancedOptions): void;
  recordId?: RecordIDPolicy;
  setRecordIDPolicy(policy?: RecordIDPolicy): void;
  preview: CollectionPreview | null;
  removedFields: string[];
  destructiveConfirmed: boolean;
  applyBlocked: boolean;
  editingBlocked: boolean;
  pending: boolean;
  error: string | null;
  deleteTarget: DeleteTarget | null;
  deleteConfirmText: string;
  deleteConfirmed: boolean;
  deletePending: boolean;
  deleteError: string | null;
  recovery: OperationRecovery | null;
  indexes: CompoundIndex[];
  indexesTouched: boolean;
  setIndexes(indexes: CompoundIndex[]): void;
  view?: ViewDefinition;
  setView(view?: ViewDefinition): void;
  viewSources: CollectionDefinition[];
  isView: boolean;
  isSQLView: boolean;
  rename(name: string): void;
  editField(index: number, edit: DraftEdit): void;
  addField(): void;
  removeField(index: number): void;
  confirmDestructive(confirmed: boolean): void;
  confirmDeleteName(typed: string): void;
  requestPreview(): void;
  requestApply(): void;
  requestDeletePlan(): void;
  cancelDelete(): void;
  requestDelete(): void;
}
