export type FieldType = "boolean" | "integer" | "bigint" | "numeric" | "text" | "timestamptz" | "uuid" | "jsonb" | "relation" | "file" | "editor" | "geoPoint" | "autodate" | "password";
export type FieldOptions = {
    maxSize?: number;
    convertURLs?: boolean;
    onCreate?: boolean;
    onUpdate?: boolean;
    minLength?: number;
    maxLength?: number;
    pattern?: string;
    autogeneratePattern?: string;
    format?: "email" | "url";
    allowedDomains?: string[];
    deniedDomains?: string[];
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
    hidden?: boolean;
    presentable?: boolean;
    help?: string;
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
/** Omitted policies use UUIDs when creating a collection. A bare text policy
 * uses 15 lowercase letters/digits. Custom text policies require all validation
 * options below; without autogeneratePattern, callers supply the record ID.
 * A collection's policy is immutable: omit it on updates or repeat its value. */
export type RecordIDPolicy = {
    kind: "uuid";
    minLength?: never;
    maxLength?: never;
    pattern?: never;
    autogeneratePattern?: never;
} | {
    kind: "text";
    minLength?: never;
    maxLength?: never;
    pattern?: never;
    autogeneratePattern?: never;
} | {
    kind: "text";
    minLength: number;
    maxLength: number;
    pattern: string;
    autogeneratePattern?: string;
};
export type CollectionDefinition = {
    id: string;
    name: string;
    tableName: string;
    recordId?: RecordIDPolicy;
    fields: FieldDefinition[];
    indexes?: CompoundIndex[];
    view?: ViewInput;
};
export type IndexKey = {
    field?: string;
    expression?: string;
    collation?: "C" | "POSIX";
    order?: "asc" | "desc";
};
/** fields, keys, and rawSql are mutually exclusive index definitions. */
export type CompoundIndex = {
    id?: string;
    name: string;
    unique: boolean;
    fields?: string[];
    keys?: IndexKey[];
    where?: string;
    rawSql?: string;
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
    recordId?: RecordIDPolicy;
    fields?: FieldInput[];
    indexes?: CompoundIndex[];
    view?: ViewInput;
} | {
    operation: "update";
    name: string;
    collectionId?: string;
    recordId?: RecordIDPolicy;
    fields: FieldInput[];
    indexes?: CompoundIndex[];
    view?: never;
} | {
    operation: "update";
    name: string;
    collectionId?: string;
    recordId?: RecordIDPolicy;
    view: ViewInput;
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
        recordId?: RecordIDPolicy;
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
export type SchemaDocumentCollection = {
    name: string;
    type?: "base" | "auth" | "view";
    recordId?: RecordIDPolicy;
    fields?: FieldInput[];
    indexes?: CompoundIndex[];
    view?: ViewInput;
    dependsOn?: string[];
    userAccess: boolean;
    rules: Record<string, unknown>;
};
export type SchemaDocumentAuthPolicy = {
    passwordEnabled: boolean;
    minimumPasswordBytes: number;
    maximumPasswordBytes: number;
    passwordPattern: string;
    identityFields: string[];
    mfaEnabled: boolean;
    mfaDurationSeconds: number;
    mfaRule: string;
    otpEnabled: boolean;
    otpDurationSeconds: number;
    otpMaxAttempts: number;
};
/** Authentication settings paired by name with a type: "auth" collection. */
export type SchemaDocumentAuthCollection = {
    name: string;
    registrationEnabled: boolean;
    recordId?: RecordIDPolicy;
    fields?: FieldInput[];
    rules: Record<string, string | null>;
    policy: SchemaDocumentAuthPolicy;
    tokenTTLs?: Record<string, number>;
};
export type SchemaDocument = {
    format: "pgbase.schema/v1";
    collections: SchemaDocumentCollection[];
    authCollections?: SchemaDocumentAuthCollection[];
    deleteMissing?: boolean;
};
export type AuthCollectionScaffold = {
    collection: SchemaDocumentCollection;
    authCollection: SchemaDocumentAuthCollection;
};
export type CollectionScaffolds = {
    base: SchemaDocumentCollection;
    auth: AuthCollectionScaffold;
    view: SchemaDocumentCollection;
};
export type CollectionTruncateResult = {
    name: string;
    truncated: true;
    recordsDeleted: number;
    filesDeleted: number;
    relationsDeleted: number;
};
export type SchemaImportPlan = {
    baseVersion: number;
    checksum: string;
    policyChecksum: string;
    changes: SchemaPlan[];
    document: SchemaDocument;
    operationId?: string;
    idempotencyKey?: string;
};
export type SchemaImportResult = {
    operationId: string;
    status: "running" | "unknown" | "committed" | "failed";
    version?: number;
    applied?: number;
};
export type AdministratorSession = {
    administrator: {
        id: string;
        email: string;
        createdAt: string;
    };
    expiresAt: string;
};
export type ImpersonatedUserSession = {
    token: string;
    user: {
        id: string;
        email: string;
        verified: boolean;
        createdAt: string;
    };
    expiresAt: string;
    impersonated: true;
    impersonatingAdministratorId: string;
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
export type FileStorageSettings = {
    driver: "local" | "s3";
    bucket: string;
    region: string;
    endpoint: string;
    accessKey: string;
    usePathStyle: boolean;
    secretConfigured: boolean;
    revision: number;
};
export type FileStorageUpdate = {
    driver: "local" | "s3";
    bucket: string;
    region: string;
    endpoint: string;
    accessKey: string;
    usePathStyle: boolean;
    revision: number;
    secretKey?: string;
    clearSecret?: boolean;
};
export type FileStorageMigrationInput = Omit<FileStorageUpdate, "revision">;
export type FileStorageMigration = {
    id: string;
    status: "preview" | "copying" | "canceling" | "canceled" | "failed" | "ready" | "activating" | "committed";
    copiedObjects: number;
    totalObjects: number;
    failure?: string;
    createdAt?: string;
    updatedAt?: string;
};
export type BackupStorageSettings = {
    driver: "local" | "s3";
    bucket: string;
    prefix: string;
    region: string;
    endpoint: string;
    accessKey: string;
    usePathStyle: boolean;
    retention: number;
    secretConfigured: boolean;
    revision: number;
};
export type BackupStorageUpdate = Omit<BackupStorageSettings, "secretConfigured"> & {
    secretKey?: string;
    clearSecret?: boolean;
};
export type AppleClientSecretInput = {
    clientId: string;
    teamId: string;
    keyId: string;
    privateKey: string;
    duration: number;
};
export type AdministratorAccount = {
    id: string;
    email: string;
    active: boolean;
    createdAt: string;
    updatedAt: string;
};
export type AdministratorAccountPage = {
    items: AdministratorAccount[];
    nextAfter?: string;
};
export type BackupEntry = {
    id: string;
    createdAt: string;
    size: number;
    postgresqlMajor: number;
    sha256?: string;
    storageFormat?: string;
};
export type BackupJob = {
    id: string;
    operation: "create" | "restore";
    backupId: string;
    status: "running" | "completed" | "failed";
    phase: "create" | "restore" | "completed" | "failed" | "interrupted";
    error?: string;
    createdAt: string;
    updatedAt: string;
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
/** A preview plus the exact JSON document that produced it. The apply body is
 * built from this private snapshot so large JSON integers survive preview. */
export declare class ApprovedSchemaImportPlan implements SchemaImportPlan {
    #private;
    readonly baseVersion: number;
    readonly checksum: string;
    readonly policyChecksum: string;
    readonly changes: SchemaPlan[];
    readonly document: SchemaDocument;
    readonly operationId: `${string}-${string}-${string}-${string}-${string}`;
    readonly idempotencyKey: `${string}-${string}-${string}-${string}-${string}`;
    constructor(preview: SchemaImportPlan, documentJSON: string);
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
    generateAppleClientSecret(input: AppleClientSecretInput, signal?: AbortSignal): Promise<{
        secret: string;
    }>;
    scaffolds(signal?: AbortSignal): Promise<CollectionScaffolds>;
    truncateCollection(name: string, signal?: AbortSignal): Promise<CollectionTruncateResult>;
    plan(change: CollectionChange, signal?: AbortSignal): Promise<ApprovedSchemaPlan>;
    apply(plan: ApprovedSchemaPlan, signal?: AbortSignal): Promise<CollectionDefinition | SchemaOperation | {
        deleted: boolean;
        name: string;
    }>;
    operation(id: string, signal?: AbortSignal): Promise<SchemaOperation>;
    exportSchema(signal?: AbortSignal): Promise<SchemaDocument>;
    exportSchemaJSON(signal?: AbortSignal): Promise<string>;
    previewSchemaImport(document: SchemaDocument, signal?: AbortSignal): Promise<ApprovedSchemaImportPlan>;
    previewSchemaImportJSON(documentJSON: string, signal?: AbortSignal): Promise<ApprovedSchemaImportPlan>;
    applySchemaImport(plan: SchemaImportPlan, signal?: AbortSignal): Promise<SchemaImportResult>;
    schemaImportOperation(id: string, signal?: AbortSignal): Promise<SchemaImportResult>;
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
    fileStorageSettings(signal?: AbortSignal): Promise<FileStorageSettings>;
    updateFileStorage(input: FileStorageUpdate, signal?: AbortSignal): Promise<FileStorageSettings>;
    testFileStorage(signal?: AbortSignal): Promise<void>;
    backupStorageSettings(signal?: AbortSignal): Promise<BackupStorageSettings>;
    updateBackupStorage(input: BackupStorageUpdate, signal?: AbortSignal): Promise<BackupStorageSettings>;
    testBackupStorage(signal?: AbortSignal): Promise<void>;
    administratorAccounts(options?: {
        limit?: number;
        after?: string;
    }, signal?: AbortSignal): Promise<AdministratorAccountPage>;
    createAdministrator(email: string, password: string, signal?: AbortSignal): Promise<AdministratorAccount>;
    setAdministratorActive(id: string, active: boolean, signal?: AbortSignal): Promise<AdministratorAccount>;
    setAdministratorPassword(id: string, newPassword: string, signal?: AbortSignal): Promise<void>;
    revokeAdministratorSessions(id: string, signal?: AbortSignal): Promise<void>;
    backups(signal?: AbortSignal): Promise<{
        items: BackupEntry[];
    }>;
    createBackup(signal?: AbortSignal): Promise<BackupJob>;
    uploadBackup(archive: Blob, signal?: AbortSignal): Promise<BackupEntry>;
    downloadBackup(id: string, signal?: AbortSignal): Promise<Blob>;
    deleteBackup(id: string, signal?: AbortSignal): Promise<void>;
    restoreBackup(id: string, signal?: AbortSignal): Promise<BackupJob>;
    backupJob(id: string, signal?: AbortSignal): Promise<BackupJob>;
    previewFileStorageMigration(input: FileStorageMigrationInput, signal?: AbortSignal): Promise<FileStorageMigration>;
    startFileStorageMigration(input: FileStorageMigrationInput, operationID: string, signal?: AbortSignal): Promise<FileStorageMigration>;
    fileStorageMigration(id: string, signal?: AbortSignal): Promise<FileStorageMigration>;
    latestFileStorageMigration(signal?: AbortSignal): Promise<FileStorageMigration>;
    resumeFileStorageMigration(id: string, signal?: AbortSignal): Promise<void>;
    cancelFileStorageMigration(id: string, signal?: AbortSignal): Promise<void>;
    impersonateUser(id: string, durationSeconds?: number, signal?: AbortSignal): Promise<ImpersonatedUserSession>;
    private adopt;
    private request;
    private requestText;
    private requestBlob;
}
