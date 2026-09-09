export type FieldType = "boolean" | "integer" | "bigint" | "numeric" | "text" | "timestamptz" | "uuid" | "jsonb" | "relation" | "file";
export type FieldOptions = {
    minLength?: number;
    maxLength?: number;
    pattern?: string;
    format?: "email" | "url";
    allowedDomains?: string[];
    allowedValues?: string[];
    min?: string;
    max?: string;
    minSelect?: number;
    maxSelect?: number;
    maxFileSize?: number;
    mimeTypes?: string[];
    thumbs?: string[];
};
export type RelationInput = {
    targetCollection: string;
    cardinality: "single" | "multiple";
    minSelect: number;
    maxSelect: number;
    onDelete: "restrict" | "cascade" | "set_null";
};
export type FieldInput = {
    id?: string;
    name: string;
    type: FieldType;
    required?: boolean;
    unique?: boolean;
    indexed?: boolean;
    default?: unknown;
    relation?: RelationInput;
    options?: FieldOptions;
};
export type FieldDefinition = Omit<FieldInput, "relation"> & {
    id: string;
    columnName: string;
    ordinal: number;
    relation?: Omit<RelationInput, "targetCollection"> & {
        targetCollectionId: string;
    };
};
export type CollectionDefinition = {
    id: string;
    name: string;
    tableName: string;
    fields: FieldDefinition[];
    indexes?: CompoundIndex[];
    view?: ViewInput;
};
export type CompoundIndex = {
    id?: string;
    name: string;
    fields: string[];
    unique: boolean;
};
export type ViewFilter = {
    op: "all" | "any" | "eq" | "ne" | "lt" | "lte" | "gt" | "gte";
    args?: ViewFilter[];
    field?: string;
    value?: unknown;
};
export type SQLViewInput = {
    sql: string;
    dependencies?: string[];
    source?: never;
    projection?: never;
    filter?: never;
};
export type ViewInput = {
    source: string;
    projection: string[];
    filter?: ViewFilter;
    sql?: never;
} | SQLViewInput;
export type CollectionChange = {
    operation: "create";
    name: string;
    fields?: FieldInput[];
    indexes?: CompoundIndex[];
    view?: ViewInput;
} | {
    operation: "update";
    name: string;
    fields: FieldInput[];
    indexes?: CompoundIndex[];
    view?: never;
} | {
    operation: "update";
    name: string;
    view: SQLViewInput;
    fields?: never;
    indexes?: never;
} | {
    operation: "delete";
    name: string;
    fields?: never;
    indexes?: never;
    view?: never;
};
export type SchemaPlan = {
    baseVersion: number;
    checksum: string;
    definition: {
        name: string;
        tableName: string;
        fields: Array<FieldInput & {
            ordinal: number;
        }>;
        indexes?: CompoundIndex[];
        view?: ViewInput;
    };
    sql: string[];
};
export type SchemaOperation = {
    operationId: string;
    status: "running" | "unknown" | "committed" | "failed";
    failureCode?: string;
};
export type AdministratorSession = {
    administrator: {
        id: string;
        email: string;
        createdAt: string;
    };
    expiresAt: string;
};
export type ScheduledJob = {
    id: string;
    name: string;
    schedule: string;
    enabled: boolean;
    registered: boolean;
    running: boolean;
    nextRunAt: string;
    lastStartedAt?: string;
    lastFinishedAt?: string;
    attempts: number;
    failed: boolean;
};
export type RequestLogPage = {
    items: Array<{
        id: number;
        at: string;
        method: string;
        route: string;
        status: number;
        durationMs: number;
    }>;
    nextBefore?: number;
    oldestId: number;
};
export declare class AdminAPIError extends Error {
    readonly status: number;
    readonly code: string;
    readonly details: Record<string, string[]>;
    constructor(status: number, code: string, details?: Record<string, string[]>);
}
/** The preview binds the exact request body and replay identity. Inspect the
 * copy returned by preview; editing it cannot change what apply submits. */
export declare class ApprovedSchemaPlan {
    #private;
    constructor(change: CollectionChange, plan: SchemaPlan);
    get preview(): SchemaPlan;
    get operationID(): string;
    request(): {
        body: string;
        headers: Record<string, string>;
    };
}
/** Browser administrator client. Cookies are managed by the browser and CSRF
 * tokens stay in memory. Use a same-origin URL; a public user token is a
 * separate identity and is never attached to administrator requests. */
export declare class PgBaseAdminClient {
    #private;
    readonly baseURL: string;
    constructor(baseURL: string, options?: {
        fetch?: typeof fetch;
    });
    login(email: string, password: string, signal?: AbortSignal): Promise<AdministratorSession>;
    resume(signal?: AbortSignal): Promise<AdministratorSession>;
    logout(signal?: AbortSignal): Promise<void>;
    catalog(signal?: AbortSignal): Promise<{
        version: number;
        collections: Record<string, CollectionDefinition>;
    }>;
    plan(change: CollectionChange, signal?: AbortSignal): Promise<ApprovedSchemaPlan>;
    apply(plan: ApprovedSchemaPlan, signal?: AbortSignal): Promise<CollectionDefinition | SchemaOperation | {
        deleted: boolean;
        name: string;
    }>;
    operation(id: string, signal?: AbortSignal): Promise<SchemaOperation>;
    logs(options?: {
        before?: number;
        statusClass?: number;
    }, signal?: AbortSignal): Promise<RequestLogPage>;
    jobs(signal?: AbortSignal): Promise<{
        items: ScheduledJob[];
        workerEnabled: boolean;
    }>;
    configureJob(id: string, schedule: string, enabled: boolean, signal?: AbortSignal): Promise<void>;
    queueJob(id: string, signal?: AbortSignal): Promise<void>;
    private adopt;
    private request;
}
