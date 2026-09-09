const pgbase = require("pgbase");

exports.run = function run(run) {
  const result = pgbase.records.create({
    collection: "n6_tasks",
    input: {
      title: "delivery " + run.jobId,
      metadata: { source: "job", logicalRun: run.jobId },
    },
    idempotencyKey: "n6-delivery-" + run.jobId,
  });
  if (String(run.fenceGeneration) === "1") {
    throw new Error("intentional first-attempt failure after record commit");
  }
  if (!result.replayed) {
    throw new Error("durable retry did not replay the logical task");
  }
  return { completed: true, runKey: run.runKey };
};
