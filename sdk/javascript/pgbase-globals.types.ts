// This file is compiled directly with pgbase.d.ts in CI/audits. It exercises
// runtime globals that cannot be imported from the CommonJS pgbase module.
export {}

$app.runInTransaction((tx) => {
  const database = tx.db()
  database.exec("select 1")
  tx.runInTransaction((nested) => {
    const sameSurface: typeof tx = nested
    void sameSurface
  })
})

const bytes: Uint8Array = $os.readFile("fixture.bin")
const commandBytes: Uint8Array = $os.cmd("echo", "ok").output()
const httpBody: number[] = $http.send({ url: "https://example.test" }).body
const publicError: ApiError = new NotFoundError("missing", { id: "one" })
$mails.sendRecordOTP($app, new Record("users"), "otp", "password")
onRecordCreateRequest((event) => {
  event.next()
  const collection: string = event.data.collection
  const requestID: string = event.data.requestID
  const input = event.data.input
  void collection
  void requestID
  void input
}, "users")
onRecordAfterCreateError((event) => {
  event.next()
  const operation: "create" | "update" | "delete" = event.data.operation
  const recordID: string = event.data.recordID
  void operation
  void recordID
})
onMailerSend((event) => {
  event.next()
  const subject: string = event.data.subject
  void subject
})
onBootstrap((event) => {
  event.next()
  const mode: "embedded" | "server" = event.data.mode
  void mode
})
onBackupCreate((event) => event.next())
onRecordModelCreate((event) => event.next())
onModelCreateSuccess((event) => event.next())

void bytes
void commandBytes
void httpBody
void publicError
