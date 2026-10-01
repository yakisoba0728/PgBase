package pgbase

// JavaScriptTypeDeclarations returns the declarations for the synchronous
// native CommonJS module exposed as require("pgbase").
func JavaScriptTypeDeclarations() string { return javaScriptTypeDeclarations }

const javaScriptTypeDeclarations = `declare module "pgbase" {
  export interface ExactNumber { readonly __pgbaseExact: never; toString(): string }
  export type JSONScalar = null | boolean | string | number | ExactNumber
  export type JSONValue = JSONScalar | JSONValue[] | { [name: string]: JSONValue }
  export interface RecordInput { [name: string]: JSONValue }
  export interface RecordResult {
    data?: { [name: string]: JSONValue }
    etag?: string
    version?: ExactNumber
    eventId?: string
    replayed?: boolean
    compatibility?: string
  }
  export interface RecordPage { items: Array<{ [name: string]: JSONValue }>; nextCursor?: string }
  export interface BatchOperation {
    operation: "create" | "update" | "upsert" | "delete"
    collection: string
    id?: string
    input?: RecordInput
    idempotencyKey?: string
    ifMatch?: string
  }
  export interface FileResult {
    id?: string
    recordId?: string
    field?: string
    filename?: string
    contentType?: string
    size?: ExactNumber
    sha256?: string
    token?: string
    dataBase64?: string
    handle?: string
    eof?: boolean
    closed?: boolean
    deleted?: boolean
    items?: FileResult[]
  }
  export interface RouteRequest {
    method: string
    path: string
    pathValues: Record<string, string>
    query: Record<string, string[]>
    headers: Record<string, string>
    principal?: { kind: "user" | "administrator" | "service"; id: string; authCollectionId?: string }
    json?: JSONValue
    body?: string
  }
  export interface RouteResponse {
    status: number
    headers?: Record<string, string>
    json?: JSONValue
    body?: string
  }
  export interface HookInvocation {
    event: string
    phase: "before" | "after"
    data: { [name: string]: JSONValue }
    failed: boolean
    state?: JSONValue
  }
  export interface HookResult { patch?: { [name: string]: JSONValue }; state?: JSONValue }
  export interface JobRun { jobId: string; name: string; fenceGeneration: ExactNumber; runKey: string; scheduledAt: string; occurrenceKey: string }
  export interface MigrationStatement { sql: string; args?: JSONValue[] }
  export interface DatabaseStatement extends MigrationStatement { query?: boolean }
  const pgbase: {
    exact(value: string | number): ExactNumber
    records: {
      list(input: { collection: string; cursor?: string; limit?: number; filter?: string; sort?: string; expand?: string }): RecordPage
      get(input: { collection: string; id: string; expand?: string }): RecordResult
      create(input: { collection: string; input: RecordInput; idempotencyKey: string }): RecordResult
      update(input: { collection: string; id: string; input: RecordInput; ifMatch: string }): RecordResult
      delete(input: { collection: string; id: string; ifMatch: string }): RecordResult
      batch(input: { operations: BatchOperation[] }): { results: RecordResult[] }
    }
    mail: { send(input: { to: string; subject: string; text: string; html?: string }): { sent: true } }
    database: { transaction(input: { statements: DatabaseStatement[] }): { results: Array<{ rowsAffected?: ExactNumber; items?: Array<Record<string, JSONValue>> }> } }
    objects: {
      write(input: { key: string; data: string }): { written: true; size: ExactNumber }
      read(input: { key: string }): { data: string; size: ExactNumber }
      delete(input: { key: string }): { deleted: true }
      list(input: { prefix?: string; cursor?: string; limit?: number }): { objects: Array<{ key:string; size:ExactNumber; updatedAt:string }>; nextCursor?: string }
      copy(input: { sourceKey: string; destinationKey: string }): { copied: true; size: ExactNumber }
    }
    files: {
      list(input: { collection: string; recordId: string; field: string }): FileResult
      /** Fails with file_stream_active until an open handle is closed. */
      upload(input: { collection: string; recordId: string; field: string; filename: string; contentType: string; dataBase64: string; sha256?: string }): FileResult
      delete(input: { fileId: string }): { deleted: true }
      token(input: { fileId: string; variant?: "original" | "thumbnail" }): { id: string; token: string }
      /** Fails with file_stream_active until an open handle is closed. */
      read(input: { fileId: string; variant?: "original" | "thumbnail"; token?: string }): FileResult
      /** Only one active file stream is allowed per invocation. */
      open(input: { fileId: string; variant?: "original" | "thumbnail"; token?: string }): FileResult & { handle: string }
      readChunk(input: { handle: string; maximumBytes?: number }): FileResult
      close(input: { handle: string }): { closed: true }
    }
    migration: {
      exec(input: MigrationStatement): { rowsAffected: ExactNumber }
      query(input: MigrationStatement): { rows: Array<Record<string, JSONValue>> }
      applySnapshot(input: { before: JSONValue; after: JSONValue }): { applied: true }
    }
    log: {
      debug(input: { message: string; fields?: Record<string, string> }): { logged: true }
      info(input: { message: string; fields?: Record<string, string> }): { logged: true }
      warn(input: { message: string; fields?: Record<string, string> }): { logged: true }
      error(input: { message: string; fields?: Record<string, string> }): { logged: true }
    }
  }
  global {
    function routerAdd(method:string,path:string,handler:(event:RequestEvent)=>JSONValue,...middlewares:Array<(next:Function)=>Function>):void
    function routerUse(...middlewares:Array<(next:Function)=>Function>):void
    function cronAdd(name:string,schedule:string,handler:(run:JobRun)=>void):void
    function cronRemove(name:string):void
    interface HookEvent<T extends object=Record<string,JSONValue>> { event:string; phase:"before"|"after"; data:T; failed:boolean; state?:JSONValue; next():void }
    type HookRegistrar<T extends object=Record<string,JSONValue>> = (handler:(event:HookEvent<T>)=>JSONValue|void,...tags:string[])=>void
    interface RecordRequestHookData { actor:{kind:string;id:string}; requestID:string; collection:string; recordID:string; idempotencyKey:string; input:{[name:string]:JSONValue}; result?:RecordResult }
    interface RecordMutationHookData { eventID:string; collection:string; recordID:string; operation:"create"|"update"|"delete"; version:number; actorID:string }
    interface MailHookData { to:string; subject:string; text:string; html:string }
    interface ApplicationHookData { mode:"embedded"|"server"; address:string }
    const onMailerSend:HookRegistrar<MailHookData>
    const onMailerRecordOTPSend:HookRegistrar, onMailerRecordVerificationSend:HookRegistrar, onMailerRecordPasswordResetSend:HookRegistrar, onMailerRecordEmailChangeSend:HookRegistrar, onMailerRecordAuthAlertSend:HookRegistrar
    const onRecordCreateRequest:HookRegistrar<RecordRequestHookData>
    const onRecordsListRequest:HookRegistrar, onRecordViewRequest:HookRegistrar, onRecordUpdateRequest:HookRegistrar, onRecordDeleteRequest:HookRegistrar, onBatchRequest:HookRegistrar, onFileDownloadRequest:HookRegistrar, onFileTokenRequest:HookRegistrar
    const onBootstrap:HookRegistrar<ApplicationHookData>
    const onSettingsListRequest:HookRegistrar, onSettingsUpdateRequest:HookRegistrar, onSettingsReload:HookRegistrar, onRealtimeConnectRequest:HookRegistrar, onRealtimeSubscribeRequest:HookRegistrar, onRealtimeMessageSend:HookRegistrar, onServe:HookRegistrar, onTerminate:HookRegistrar
    const onCollectionsListRequest:HookRegistrar, onCollectionViewRequest:HookRegistrar, onCollectionCreateRequest:HookRegistrar, onCollectionUpdateRequest:HookRegistrar, onCollectionDeleteRequest:HookRegistrar, onRecordEnrich:HookRegistrar, onCollectionsImportRequest:HookRegistrar
    const onModelValidate:HookRegistrar, onModelCreate:HookRegistrar, onModelCreateExecute:HookRegistrar, onModelAfterCreateSuccess:HookRegistrar, onModelAfterCreateError:HookRegistrar, onModelUpdate:HookRegistrar, onModelUpdateExecute:HookRegistrar, onModelAfterUpdateSuccess:HookRegistrar, onModelAfterUpdateError:HookRegistrar, onModelDelete:HookRegistrar, onModelDeleteExecute:HookRegistrar, onModelAfterDeleteSuccess:HookRegistrar, onModelAfterDeleteError:HookRegistrar
    const onRecordAfterCreateError:HookRegistrar<RecordMutationHookData>
    const onRecordValidate:HookRegistrar, onRecordCreate:HookRegistrar, onRecordCreateExecute:HookRegistrar, onRecordAfterCreateSuccess:HookRegistrar, onRecordUpdate:HookRegistrar, onRecordUpdateExecute:HookRegistrar, onRecordAfterUpdateSuccess:HookRegistrar, onRecordAfterUpdateError:HookRegistrar, onRecordDelete:HookRegistrar, onRecordDeleteExecute:HookRegistrar, onRecordAfterDeleteSuccess:HookRegistrar, onRecordAfterDeleteError:HookRegistrar
    const onCollectionValidate:HookRegistrar, onCollectionCreate:HookRegistrar, onCollectionCreateExecute:HookRegistrar, onCollectionAfterCreateSuccess:HookRegistrar, onCollectionAfterCreateError:HookRegistrar, onCollectionUpdate:HookRegistrar, onCollectionUpdateExecute:HookRegistrar, onCollectionAfterUpdateSuccess:HookRegistrar, onCollectionAfterUpdateError:HookRegistrar, onCollectionDelete:HookRegistrar, onCollectionDeleteExecute:HookRegistrar, onCollectionAfterDeleteSuccess:HookRegistrar, onCollectionAfterDeleteError:HookRegistrar
    const onRecordAuthRequest:HookRegistrar, onRecordAuthRefreshRequest:HookRegistrar, onRecordAuthWithPasswordRequest:HookRegistrar, onRecordAuthWithOAuth2Request:HookRegistrar, onRecordRequestOTPRequest:HookRegistrar, onRecordAuthWithOTPRequest:HookRegistrar, onRecordRequestPasswordResetRequest:HookRegistrar, onRecordConfirmPasswordResetRequest:HookRegistrar, onRecordRequestVerificationRequest:HookRegistrar, onRecordConfirmVerificationRequest:HookRegistrar, onRecordRequestEmailChangeRequest:HookRegistrar, onRecordConfirmEmailChangeRequest:HookRegistrar
    const onMailSend:HookRegistrar, onMailOtp:HookRegistrar, onMailVerification:HookRegistrar, onMailPasswordReset:HookRegistrar, onMailEmailChange:HookRegistrar, onRecordList:HookRegistrar, onRecordView:HookRegistrar, onRecordBatch:HookRegistrar, onFileDownload:HookRegistrar, onFileToken:HookRegistrar, onSettingsList:HookRegistrar, onSettingsUpdate:HookRegistrar, onRealtimeConnect:HookRegistrar, onRealtimeSubscribe:HookRegistrar, onRealtimeMessage:HookRegistrar, onAppBoot:HookRegistrar, onAppServe:HookRegistrar, onAppTerminate:HookRegistrar, onCollectionRequestList:HookRegistrar, onCollectionRequestView:HookRegistrar, onCollectionRequestCreate:HookRegistrar, onCollectionRequestUpdate:HookRegistrar, onCollectionRequestDelete:HookRegistrar, onSchemaImportPreview:HookRegistrar, onSchemaImportApply:HookRegistrar, onAuthRefresh:HookRegistrar, onAuthPassword:HookRegistrar, onAuthOauthBegin:HookRegistrar, onAuthOauthComplete:HookRegistrar, onAuthOtpRequest:HookRegistrar, onAuthOtpComplete:HookRegistrar, onAuthPasswordResetRequest:HookRegistrar, onAuthPasswordResetConfirm:HookRegistrar, onAuthVerificationRequest:HookRegistrar, onAuthVerificationConfirm:HookRegistrar, onAuthEmailChangeRequest:HookRegistrar, onAuthEmailChangeConfirm:HookRegistrar, onAuthAlert:HookRegistrar
    const onRequest:HookRegistrar, onRecordBefore:HookRegistrar, onRecordAfter:HookRegistrar, onMailSmtpTest:HookRegistrar, onFileUpload:HookRegistrar, onFileDelete:HookRegistrar, onFileTransform:HookRegistrar, onAuthImpersonate:HookRegistrar
    const onStorageSettingsList:HookRegistrar, onStorageSettingsUpdate:HookRegistrar, onStorageSettingsTest:HookRegistrar, onBackupCreate:HookRegistrar, onBackupRestore:HookRegistrar, onBackupDelete:HookRegistrar, onObjectWrite:HookRegistrar, onObjectDelete:HookRegistrar
    const onModelExecute:HookRegistrar, onModelSuccess:HookRegistrar, onModelError:HookRegistrar, onModelCreateSuccess:HookRegistrar, onModelCreateError:HookRegistrar, onModelUpdateSuccess:HookRegistrar, onModelUpdateError:HookRegistrar, onModelDeleteSuccess:HookRegistrar, onModelDeleteError:HookRegistrar
    const onRecordModelValidate:HookRegistrar, onRecordModelCreate:HookRegistrar, onRecordModelCreateExecute:HookRegistrar, onRecordModelCreateSuccess:HookRegistrar, onRecordModelCreateError:HookRegistrar, onRecordModelUpdate:HookRegistrar, onRecordModelUpdateExecute:HookRegistrar, onRecordModelUpdateSuccess:HookRegistrar, onRecordModelUpdateError:HookRegistrar, onRecordModelDelete:HookRegistrar, onRecordModelDeleteExecute:HookRegistrar, onRecordModelDeleteSuccess:HookRegistrar, onRecordModelDeleteError:HookRegistrar
    const onCollectionModelValidate:HookRegistrar, onCollectionModelCreate:HookRegistrar, onCollectionModelCreateExecute:HookRegistrar, onCollectionModelCreateSuccess:HookRegistrar, onCollectionModelCreateError:HookRegistrar, onCollectionModelUpdate:HookRegistrar, onCollectionModelUpdateExecute:HookRegistrar, onCollectionModelUpdateSuccess:HookRegistrar, onCollectionModelUpdateError:HookRegistrar, onCollectionModelDelete:HookRegistrar, onCollectionModelDeleteExecute:HookRegistrar, onCollectionModelDeleteSuccess:HookRegistrar, onCollectionModelDeleteError:HookRegistrar
    interface PBRecord { id:string; readonly collectionName:string; get(name:string):JSONValue; set(name:string,value:JSONValue):this; unset(name:string):this; load(value:{[key:string]:JSONValue}):this; original():{[key:string]:JSONValue}; export():{[key:string]:JSONValue} }
    const Record: { new(collection:string|Collection,data?:{[key:string]:JSONValue}):PBRecord }
    class Collection { constructor(value:string|{[key:string]:JSONValue}); name:string; readonly fields:Field[]; export():{[key:string]:JSONValue} }
    class FieldsList extends Array<Field> { constructor(values?:Array<Field|{[key:string]:JSONValue}>); getByName(name:string):Field|undefined; add(...fields:Field[]):this; removeByName(name:string):this; export():Array<{[key:string]:JSONValue}> }
    class Field { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    class TextField extends Field {} class NumberField extends Field {} class BoolField extends Field {} class EmailField extends Field {} class URLField extends Field {} class EditorField extends Field {} class PasswordField extends Field {} class DateField extends Field {} class SelectField extends Field {} class JSONField extends Field {} class FileField extends Field {} class RelationField extends Field {} class AutodateField extends Field {} class GeoPointField extends Field {}
    class Command { constructor(value?:{[key:string]:JSONValue}) }
    class Middleware { constructor(handler:(event:RequestEvent)=>JSONValue,priority?:number,id?:string); run(event:RequestEvent):JSONValue }
    class Timezone { constructor(name?:string); string():string; toString():string }
    interface PBFormData { append(name:string,value:JSONValue,filename?:string):void; set(name:string,value:JSONValue,filename?:string):void; get(name:string):JSONValue; getAll(name:string):JSONValue[]; has(name:string):boolean; delete(name:string):void }
    const FormData: { new():PBFormData }
    interface SQLExpression { readonly op:string; readonly args:JSONValue[]; match(...flags:boolean[]):this; build(db?:JSONValue,params?:Record<string,JSONValue>):string }
    const $dbx: { exp(sql:string,params?:Record<string,JSONValue>):SQLExpression; hashExp(value:Record<string,JSONValue>):SQLExpression; not(value:SQLExpression):SQLExpression; and(...values:SQLExpression[]):SQLExpression; or(...values:SQLExpression[]):SQLExpression; in(field:string,...values:JSONValue[]):SQLExpression; notIn(field:string,...values:JSONValue[]):SQLExpression; like(field:string,...values:string[]):SQLExpression; orLike(field:string,...values:string[]):SQLExpression; notLike(field:string,...values:string[]):SQLExpression; orNotLike(field:string,...values:string[]):SQLExpression; exists(value:SQLExpression):SQLExpression; notExists(value:SQLExpression):SQLExpression; between(field:string,start:JSONValue,end:JSONValue):SQLExpression; notBetween(field:string,start:JSONValue,end:JSONValue):SQLExpression }
    class MailerMessage { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    interface PBRequestInfo { export():{[key:string]:JSONValue} }
    const RequestInfo: { new(value?:{[key:string]:JSONValue}):PBRequestInfo }
    class RequestEvent { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    const $security: { md5(value:string):string; sha256(value:string):string; sha512(value:string):string; hs256(value:string,key:string):string; hs512(value:string,key:string):string; equal(a:string,b:string):boolean; randomString(length:number):string; randomStringWithAlphabet(length:number,alphabet:string):string; createJWT(payload:Record<string,JSONValue>,key:string,seconds?:number):string; parseJWT(token:string,key:string):Record<string,JSONValue>; encrypt(value:string,key:string):string; decrypt(value:string,key:string):string }
    const $http: { send(request:{url:string;method?:string;headers?:Record<string,string>;body?:string;timeout?:number}):{statusCode:number;headers:Record<string,string[]>;cookies:Record<string,{value:string}>;body:number[];raw:string;json?:JSONValue} }
    interface OSCommandResult { output():Uint8Array; success():boolean; error():string }
    interface OSCommand { run():OSCommandResult; output():Uint8Array; success():boolean; error():string }
    const $os: { readonly args:string[]; getenv(name:string):string; exit(code?:number):{requested:true}; tempDir():string; getwd():string; readFile(path:string):Uint8Array; writeFile(path:string,data:Uint8Array|number[],mode?:number):void; cmd(name:string,...args:string[]):OSCommand; exec(name:string,...args:string[]):OSCommandResult; readDir(path:string):DirEntry[]; stat(path:string):FileInfo; truncate(path:string,size:number):void; mkdir(path:string,mode?:number):void; mkdirAll(path:string,mode?:number):void; rename(oldPath:string,newPath:string):void; remove(path:string):void; removeAll(path:string):void }
    interface DirEntry { name():string; isDir():boolean; isFile():boolean }
    interface FileInfo { name():string; size():number; isDir():boolean; modTime():string }
    interface FilesystemReader { read(maximumBytes?:number):{data:string;eof:boolean}; close():JSONValue; contentType():string; modTime():string; size():number }
    interface FilesystemWriter { write(data:string):JSONValue; writeChunk(data:string):JSONValue; close():JSONValue; commit():JSONValue; abort():JSONValue }
    /** A page from the namespace-scoped operator storage, with bridge object metadata. */
    interface FilesystemPage { objects:Array<{Key:string;Size:number;UpdatedAt:string}>; nextCursor:string }
    interface ConfiguredFilesystemSystem extends FilesystemSystem { listPage(prefix?:string,cursor?:string,limit?:number):FilesystemPage }
    /** Operations inherit the invocation context; setContext always throws filesystem_context_unsupported. */
    interface FilesystemSystem { close():JSONValue; setContext(value?:JSONValue):never; exists(key:string):boolean; attributes(key:string):{key?:string;name?:string;size:number;modTime:string;contentType?:string}; getReader(key:string):FilesystemReader; getFile(key:string):FilesystemReader; getReuploadableFile(key:string,preserveName?:boolean):JSONValue; copy(sourceKey:string,destinationKey:string):JSONValue; list(prefix?:string):JSONValue[]; upload(content:string|number[],key:string):JSONValue; uploadFile(file:JSONValue,key:string):JSONValue; uploadMultipart(file:JSONValue,key:string):JSONValue; newWriter(key:string,options?:JSONValue):FilesystemWriter; delete(key:string):JSONValue; deletePrefix(prefix:string):JSONValue; isEmptyDir(prefix:string):boolean; serve(res:JSONValue,req:JSONValue,fileKey:string,name:string):void; createThumb(originalKey:string,thumbKey:string,thumbSize:string):JSONValue }
    const $filesystem: { configured():ConfiguredFilesystemSystem; /** @deprecated Use configured(); storage arguments are rejected. */ s3():ConfiguredFilesystemSystem; local(root:string):FilesystemSystem; fileFromPath(path:string):JSONValue; fileFromBytes(bytes:string|number[],name:string):JSONValue; fileFromURL(url:string,timeout?:number):JSONValue; list(prefix?:string,cursor?:string,limit?:number):JSONValue; copy(sourceKey:string,destinationKey:string):JSONValue; read(key:string):JSONValue; write(key:string,data:string|JSONValue):JSONValue; delete(key:string):JSONValue }
    const $filepath: { base(path:string):string; clean(path:string):string; dir(path:string):string; ext(path:string):string; join(...parts:string[]):string; rel(base:string,target:string):string; isAbs(path:string):boolean }
    interface CollectionQuery { where(expression:SQLExpression):this; andWhere(expression:SQLExpression):this; orWhere(expression:SQLExpression):this; types(...types:string[]):this; orderBy(field:string):this; limit(maximum:number):this; offset(offset:number):this; all():Collection[]; one():Collection; count():number }
    interface AuthAuxiliary { id:string; type:string; provider?:string; active?:boolean; expiresAt?:string; consumedAt?:string; firstSeenAt?:string; lastSeenAt?:string; createdAt:string }
    interface App { findRecordsByFilter(collection:string,filter:string,sort?:string,limit?:number):RecordPage; findFirstRecordByFilter(collection:string,filter:string):{[key:string]:JSONValue}|undefined; findRecordById(collection:string,id:string):RecordResult; save(record:PBRecord|Collection|{[key:string]:JSONValue}):RecordResult; delete(record:PBRecord|Collection|{[key:string]:JSONValue}):RecordResult; collectionQuery():CollectionQuery; findAllExternalAuthsByRecord(record:PBRecord):AuthAuxiliary[]; findAllExternalAuthsByCollection(collection:Collection):AuthAuxiliary[]; findFirstExternalAuthByExpr(expression:SQLExpression):AuthAuxiliary; deleteAllExternalAuthsByRecord(record:PBRecord):void; findAllMFAsByRecord(record:PBRecord):AuthAuxiliary[]; findAllMFAsByCollection(collection:Collection):AuthAuxiliary[]; findMFAById(id:string):AuthAuxiliary; deleteAllMFAsByRecord(record:PBRecord):void; deleteExpiredMFAs():void; findAllOTPsByRecord(record:PBRecord):AuthAuxiliary[]; findAllOTPsByCollection(collection:Collection):AuthAuxiliary[]; findOTPById(id:string):AuthAuxiliary; deleteAllOTPsByRecord(record:PBRecord):void; deleteExpiredOTPs():void; findAllAuthOriginsByRecord(record:PBRecord):AuthAuxiliary[]; findAllAuthOriginsByCollection(collection:Collection):AuthAuxiliary[]; findAuthOriginById(id:string):AuthAuxiliary; findAuthOriginByRecordAndFingerprint(record:PBRecord,fingerprint:string):AuthAuxiliary; deleteAllAuthOriginsByRecord(record:PBRecord):void; findAuthRecordByToken(token:string,...validTypes:string[]):PBRecord; findAuthRecordByEmail(collection:string|Collection,email:string):PBRecord; runInTransaction(fn:(txApp:App)=>void):void; auxRunInTransaction(fn:(txApp:App)=>void):void; isTransactional():boolean; transactionInfo():{transactional:boolean;token?:string}; db():AppDatabase; concurrentDB():AppDatabase; nonconcurrentDB():AppDatabase; auxDB():AppDatabase; auxConcurrentDB():AppDatabase; auxNonconcurrentDB():AppDatabase }
    interface AppDatabase { transaction(statements:DatabaseStatement[]):JSONValue; exec(sql:string,args?:JSONValue[]):{rowsAffected?:ExactNumber}; query(sql:string,args?:JSONValue[]):{items:Array<Record<string,JSONValue>>}; tables():JSONValue; columns(name:string):JSONValue; indexes(name:string):JSONValue }
    interface App { saveWithContext(context:JSONValue,record:PBRecord|Collection):RecordResult; filesystem():FilesystemSystem }
    interface App { isBootstrapped():boolean; bootstrap():JSONValue; resetBootstrapState():JSONValue; dataDir():string; encryptionEnv():string; isDev():boolean; settings():JSONValue; store():{get(key:string):JSONValue;set(key:string,value:JSONValue):JSONValue;delete(key:string):JSONValue}; cron():{list():JSONValue;configure(id:string,schedule:string,timezone?:string,enabled?:boolean):JSONValue;run(id:string):JSONValue}; subscriptionsBroker():JSONValue; newBackupsFilesystem():{list():JSONValue;read(id:string):JSONValue;delete(id:string):JSONValue}; reloadSettings():JSONValue; createBackup():JSONValue; restoreBackup(id:string):JSONValue; restart():JSONValue; runSystemMigrations():JSONValue; runAppMigrations():JSONValue; runAllMigrations():JSONValue; unsafeWithoutHooks<T>(fn:()=>T):T; recordQuery(collection:string,filter?:string,sort?:string,limit?:number):JSONValue; findRecordsByIds(collection:string,ids:string[]):PBRecord[]; findAllRecords(collection:string):PBRecord[]; findFirstRecordByData(collection:string,field:string,value:JSONValue):PBRecord; countRecords(collection:string,filter?:string):number; canAccessRecord(record:PBRecord,requestInfo?:JSONValue,rule?:string):boolean; expandRecord(record:PBRecord,expand:string):PBRecord; expandRecords(records:PBRecord[],expand:string):PBRecord[]; logQuery(filter?:JSONValue):JSONValue; findLogById(id:string):JSONValue; logsStats():JSONValue; deleteOldLogs(before:string):JSONValue; findCollectionByNameOrId(name:string):Collection; findCachedCollectionByNameOrId(name:string):Collection; findAllCollections(...types:string[]):Collection[]; findCollectionReferences(collection:string|Collection):JSONValue; findCachedCollectionReferences(collection:string|Collection):JSONValue; isCollectionNameUnique(name:string,excludeId?:string):boolean; findAuthAuxiliary(collection:string,recordId:string,kind:string):AuthAuxiliary[]; deleteAuthAuxiliary(collection:string,recordId:string,kind:string,id:string):JSONValue; modelQuery(model:PBRecord|JSONValue):ModelQuery; auxModelQuery(model:PBRecord|JSONValue):ModelQuery; auxSave(record:PBRecord|Collection):RecordResult; auxSaveWithContext(context:JSONValue,record:PBRecord|Collection):RecordResult; saveNoValidate(record:PBRecord|Collection):RecordResult; saveNoValidateWithContext(context:JSONValue,record:PBRecord|Collection):RecordResult; auxSaveNoValidate(record:PBRecord|Collection):RecordResult; auxSaveNoValidateWithContext(context:JSONValue,record:PBRecord|Collection):RecordResult; auxDelete(record:PBRecord|Collection):RecordResult; auxDeleteWithContext(context:JSONValue,record:PBRecord|Collection):RecordResult; deleteWithContext(context:JSONValue,record:PBRecord|Collection):RecordResult; validate(record:PBRecord|Collection):{valid:boolean}; validateWithContext(context:JSONValue,record:PBRecord|Collection):{valid:boolean}; hasTable(name:string):boolean; auxHasTable(name:string):boolean; tableColumns(name:string):JSONValue; tableInfo(name:string):JSONValue; tableIndexes(name:string):JSONValue; deleteTable(name:string):JSONValue; deleteView(name:string):JSONValue; saveView(name:string,query:string):JSONValue; createViewFields(query:string):FieldsList; dryRunView(query:string,sampleSize?:number):JSONValue; findRecordByViewFile(view:string|JSONValue,field:string,filename:string):JSONValue; vacuum():JSONValue; auxVacuum():JSONValue; reloadCachedCollections():JSONValue; importCollections(value:JSONValue,deleteMissing?:boolean):JSONValue; importCollectionsByMarshaledJSON(value:string|JSONValue,deleteMissing?:boolean):JSONValue; syncRecordTableSchema(collection:string|Collection):JSONValue; truncateCollection(name:string|Collection):JSONValue; logger():JSONValue; mailer():JSONValue; txInfo():{transactional:boolean;token?:string}; newMailClient():JSONValue; newFilesystem():FilesystemSystem }
    interface ModelQuery { where(filter:string):this; orderBy(sort:string):this; limit(limit:number):this; all():{items:JSONValue[]}; one():JSONValue; count():number }
    const $app: App
    class ApiError extends Error { constructor(status?:number,message?:string,data?:JSONValue); status:number; data:JSONValue; rawData():JSONValue }
    class NotFoundError extends ApiError { constructor(message?:string,data?:JSONValue) } class BadRequestError extends ApiError { constructor(message?:string,data?:JSONValue) } class ForbiddenError extends ApiError { constructor(message?:string,data?:JSONValue) } class UnauthorizedError extends ApiError { constructor(message?:string,data?:JSONValue) } class TooManyRequestsError extends ApiError { constructor(message?:string,data?:JSONValue) } class InternalServerError extends ApiError { constructor(message?:string,data?:JSONValue) }
    const $mails: { sendRecordPasswordReset(app:App,record:PBRecord):JSONValue; sendRecordVerification(app:App,record:PBRecord):JSONValue; sendRecordChangeEmail(app:App,record:PBRecord,newEmail:string):JSONValue; sendRecordOTP(app:App,record:PBRecord,otpId:string,otpPassword:string):JSONValue; sendRecordAuthAlert(app:App,record:PBRecord,alertInfo:string):JSONValue }
    const $apis: { static(root:string|{root:string},indexFallback?:boolean):(event:RequestEvent)=>RouteResponse; requireGuestOnly(next:(event:RequestEvent)=>JSONValue):(event:RequestEvent)=>JSONValue; requireAuth(...collections:string[]):(next:(event:RequestEvent)=>JSONValue)=>(event:RequestEvent)=>JSONValue; requireSuperuserAuth(next:(event:RequestEvent)=>JSONValue):(event:RequestEvent)=>JSONValue; requireSuperuserOrOwnerAuth(ownerIdPathParam?:string):(next:(event:RequestEvent)=>JSONValue)=>(event:RequestEvent)=>JSONValue; skipSuccessActivityLog<T extends Function>(next:T):T; gzip(next:(event:RequestEvent)=>RouteResponse):(event:RequestEvent)=>RouteResponse; bodyLimit(limit:number,next?:(event:RequestEvent)=>JSONValue):((event:RequestEvent)=>JSONValue)|((next:(event:RequestEvent)=>JSONValue)=>(event:RequestEvent)=>JSONValue); recordAuthResponse(event:RequestEvent,record:PBRecord,method:string,meta?:JSONValue):JSONValue; enrichRecord(event:RequestEvent,record:PBRecord,...expands:string[]):PBRecord; enrichRecords(event:RequestEvent,records:PBRecord[],...expands:string[]):PBRecord[] }
  }
  export = pgbase
}
`
