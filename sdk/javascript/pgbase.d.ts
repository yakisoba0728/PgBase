declare module "pgbase" {
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
    mail: { send(input: { to: string; subject: string; text: string; html?: string; attachments?: Array<{ filename: string; contentType?: string; dataBase64: string }> }): { sent: true } }
		template: { render(input: { name: string; data: JSONValue }): { output: string } }
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
    class Record { constructor(collection:string|Collection,data?:{[key:string]:JSONValue}); id:string; readonly collectionName:string; get(name:string):JSONValue; set(name:string,value:JSONValue):this; unset(name:string):this; load(value:{[key:string]:JSONValue}):this; original():{[key:string]:JSONValue}; export():{[key:string]:JSONValue} }
    class Collection { constructor(value:string|{[key:string]:JSONValue}); name:string; readonly fields:Field[]; export():{[key:string]:JSONValue} }
    class Field { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    class TextField extends Field {} class NumberField extends Field {} class BoolField extends Field {} class EmailField extends Field {} class URLField extends Field {} class DateField extends Field {} class SelectField extends Field {} class JSONField extends Field {} class FileField extends Field {} class RelationField extends Field {} class AutodateField extends Field {}
    class MailerMessage { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    class RequestInfo { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    class RequestEvent { constructor(value?:{[key:string]:JSONValue}); export():{[key:string]:JSONValue} }
    const $security: { md5(value:string):string; sha256(value:string):string; sha512(value:string):string; hs256(value:string,key:string):string; hs512(value:string,key:string):string; equal(a:string,b:string):boolean; randomString(length:number):string; randomStringWithAlphabet(length:number,alphabet:string):string; createJWT(payload:Record<string,JSONValue>,key:string,seconds?:number):string; parseJWT(token:string,key:string):Record<string,JSONValue>; encrypt(value:string,key:string):string; decrypt(value:string,key:string):string }
    const $http: { send(request:{url:string;method?:string;headers?:Record<string,string>;body?:string}):{statusCode:number;headers:Record<string,string>;body:string} }
    const $os: { readonly args:string[]; getenv(name:string):string; exit(code?:number):{requested:true}; readFile(path:string):string; writeFile(path:string,data:string,mode?:number):void; cmd(name:string,...args:string[]):{output:string;success:boolean;error:string} }
    const $filesystem: { fileFromPath(path:string):JSONValue; fileFromBytes(bytes:string|number[],name:string):JSONValue; fileFromURL(url:string,timeout?:number):JSONValue; list(prefix?:string,cursor?:string,limit?:number):JSONValue; copy(sourceKey:string,destinationKey:string):JSONValue; read(key:string):JSONValue; write(key:string,data:string):JSONValue; delete(key:string):JSONValue }
    const $filepath: { base(path:string):string; clean(path:string):string; dir(path:string):string; ext(path:string):string; join(...parts:string[]):string; rel(base:string,target:string):string; isAbs(path:string):boolean }
    const $app: { findRecordsByFilter(collection:string,filter:string,sort?:string,limit?:number):RecordPage; findFirstRecordByFilter(collection:string,filter:string):Record<string,JSONValue>|undefined; findRecordById(collection:string,id:string):RecordResult; save(record:Record<string,JSONValue>):RecordResult; delete(record:Record<string,JSONValue>):RecordResult }
  }
  export = pgbase
}
