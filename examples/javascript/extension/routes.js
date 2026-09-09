const pgbase = require("pgbase");

exports.createTask = function createTask(request) {
  const input = request.json || {};
  const result = pgbase.records.create({
    collection: "n6_tasks",
    input: {
      title: input.title || "JavaScript task",
      metadata: input.metadata || { source: "javascript" },
    },
    idempotencyKey: input.idempotencyKey,
  });
  return {
    status: 201,
    headers: { ETag: result.etag, "X-Extension": "n6-javascript" },
    json: {
      record: result.data,
      etag: result.etag,
      eventId: result.eventId,
      replayed: result.replayed === true,
    },
  };
};

exports.getTask = function getTask(request) {
  const result = pgbase.records.get({
    collection: "n6_tasks",
    id: request.pathValues.id,
  });
  return {
    status: 200,
    headers: { ETag: result.etag, "X-Extension": "n6-javascript" },
    json: { record: result.data, etag: result.etag },
  };
};

exports.uploadFile = function uploadFile(request) {
  const input = request.json || {};
  const upload = {
    collection: "n6_tasks",
    recordId: request.pathValues.id,
    field: "attachment",
    filename: input.filename,
    contentType: input.contentType,
    dataBase64: input.dataBase64,
  };
  if (input.sha256) upload.sha256 = input.sha256;
  const file = pgbase.files.upload(upload);
  return { status: 201, json: file };
};

exports.readFile = function readFile(request) {
  const opened = pgbase.files.open({ fileId: request.pathValues.fileId });
  const chunks = [];
  try {
    for (;;) {
      const chunk = pgbase.files.readChunk({ handle: opened.handle, maximumBytes: 4096 });
      if (chunk.dataBase64) chunks.push(chunk.dataBase64);
      if (chunk.eof) break;
    }
  } finally {
    pgbase.files.close({ handle: opened.handle });
  }
  return {
    status: 200,
    json: { id: opened.id, sha256: opened.sha256, chunks: chunks },
  };
};

exports.sendMail = function sendMail(request) {
  const input = request.json || {};
  pgbase.mail.send({
    to: input.to,
    subject: input.subject,
    text: input.text,
  });
  return { status: 202, json: { sent: true } };
};
