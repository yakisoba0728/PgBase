import subprocess,json,pathlib,os,hashlib
root=pathlib.Path("/workspace/scratch/27f5e00d0baa/worktrees")
ev=root/"independent-qa/docs/workstreams/independent-platform-2026-11-16/evidence/final-master/independent"
rows=[]
for label,work in [("backend-integration-red",root/"independent-red"),("backend-integration-green",root/"independent-backend")]:
 cmd=["go","test","-race","-p=1","-tags=integration","./internal/filestore","-run","^TestStorageControllerForeignLeaseKeepsObjectNamespaceAndPublicationOwner$","-count=1","-v","-timeout=3m"]
 result=subprocess.run(cmd,cwd=work,capture_output=True,text=True)
 log=ev/(label+".log");log.write_text(result.stdout+result.stderr)
 row={"check":label,"exit_code":result.returncode,"command":cmd,"head":subprocess.check_output(["git","rev-parse","HEAD"],cwd=work,text=True).strip(),"source_tree":subprocess.check_output(["git","rev-parse","HEAD^{tree}"],cwd=work,text=True).strip(),"fixture_overlaid":label.endswith("red"),"fixture_sha256":hashlib.sha256((work/"internal/filestore/storage_lease_ownership_integration_test.go").read_bytes()).hexdigest(),"log_sha256":hashlib.sha256(log.read_bytes()).hexdigest()}
 rows.append(row);print(json.dumps(row),flush=True)
(ev/"backend-integration-execution.json").write_text(json.dumps(rows,indent=2)+"\n")
assert rows[0]["exit_code"]==1 and rows[1]["exit_code"]==0, rows

cmd=["go","test","-race","-p=1","-tags=integration","./internal/filestore","./internal/migrate","-run","^(TestStorageLeaseDoesNotExhaustRequestPoolAndLostLeaseCannotPublish|TestStorageControllerRangeRetainsLeaseUntilBodyClose|TestStagedPublicationRejectsLostOwnership|TestStorageAdmissionReleaseWaitsForServerUnlock|TestStageCleanupIntentSharesStorageTransitionLease|TestVariantRetainedIntentSurvivesLostSessionsAndLatePut|TestVariantUncertainIntentProtectsLegacyWorkerAndMigrationSource|TestVariantCleanupUpgradePreservesQueueAndReapplies)$","-count=1","-v","-timeout=5m"]
result=subprocess.run(cmd,cwd=root/"independent-backend",capture_output=True,text=True)
log=ev/"backend-adjacent-integration.log";log.write_text(result.stdout+result.stderr)
row={"check":"backend-adjacent-integration","exit_code":result.returncode,"command":cmd,"source_sha":"e59fc07668e0a282555f2f52dbddf890162254e0","source_tree":"63b3b6eb3379e32967b16d5a637e9aa1d1052f95","log_sha256":hashlib.sha256(log.read_bytes()).hexdigest()}
(ev/"backend-adjacent-integration-execution.json").write_text(json.dumps(row,indent=2)+"\n");print(json.dumps(row),flush=True)
assert result.returncode==0
