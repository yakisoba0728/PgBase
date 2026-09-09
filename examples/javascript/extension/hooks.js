const pgbase = require("pgbase");

exports.recordCreated = function recordCreated(event) {
  if (!event.failed && event.data.collection === "n6_tasks") {
    pgbase.log.info({
      message: "n6 task committed",
      fields: { requestId: event.data.requestId || "job" },
    });
  }
  return {};
};

exports.prefixMail = function prefixMail(event) {
  return {
    patch: {
      to: event.data.to,
      subject: "[N6 JavaScript] " + event.data.subject,
      text: event.data.text,
      html: event.data.html || "",
    },
  };
};
