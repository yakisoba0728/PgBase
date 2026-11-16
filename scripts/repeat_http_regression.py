#!/usr/bin/env python3
"""Reconstruct and repeat the historical HTTP comparison on one isolated host.

PGBASE_TEST_ADMIN_URL must identify a disposable PostgreSQL server. The script
uses no operational endpoint, and neither updates remote refs nor publishes.
Use --candidate REF for an explicitly broader final-runtime comparison; omit
it to isolate the two historical allocation/cursor commits.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import time


BASE = "6dd8f6449f7b6bc1a986ff7444b898776abf1c7b"
PATCHES = ["312f1c614b4c0d08c32a922dfb4fb2040ebaa3b6", "c0a37c385b5a49bbf6fd9cef624eace9b941482f"]
HARNESS = "7c2e82234e188d623181048692d78975438d81f6"
PATH = "pgbase/mixed_load_integration_test.go"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--cycles", type=int, default=500)
    parser.add_argument("--candidate")
    args = parser.parse_args()
    if not os.environ.get("PGBASE_TEST_ADMIN_URL"):
        parser.error("PGBASE_TEST_ADMIN_URL is required")
    if not 100 <= args.cycles <= 1000:
        parser.error("cycles must be in [100,1000]")
    repo = Path(subprocess.check_output(["git","rev-parse","--show-toplevel"],text=True).strip())
    output = args.output.resolve()
    output.mkdir(parents=True,exist_ok=False)
    temporary = Path(tempfile.mkdtemp(prefix="pgbase-http-abba-"))
    env = dict(os.environ, GOMAXPROCS="4", PGBASE_MIXED_CYCLES=str(args.cycles))
    def git(*command, cwd=repo):
        return subprocess.check_output(["git",*command],cwd=cwd)
    harness = git("show",HARNESS+":"+PATH)
    metadata = {"platform":platform.platform(),"go":subprocess.check_output(["go","version"],text=True).strip(),
                "cpu_count":os.cpu_count(),"gomaxprocs":4,"cycles":args.cycles,"order":["before","after","after","before"]*3,
                "baseline":BASE,"candidate":args.candidate or {"base":BASE,"patches":PATCHES},
                "harness_commit":HARNESS,"harness_sha256":hashlib.sha256(harness).hexdigest(),"started":time.time()}
    def write_metadata():
        (output/"environment.json").write_text(json.dumps(metadata,indent=2)+"\n")
    metadata["phase"]="build"
    write_metadata()
    worktrees=[]
    try:
        for side in ("before","after"):
            work=temporary/side
            ref=args.candidate if side=="after" and args.candidate else BASE
            git("worktree","add","--detach",str(work),ref)
            worktrees.append(work)
            if side=="after" and not args.candidate:
                for commit in PATCHES:
                    subprocess.run(["git","apply"],cwd=work,input=git("show",commit,"--format="),check=True)
            (work/PATH).write_bytes(harness)
            (output/f"source-{side}.patch").write_bytes(git("diff","HEAD",cwd=work))
            metadata[side+"_head"]=git("rev-parse","HEAD",cwd=work).decode().strip()
            write_metadata()
            subprocess.run(["go","test","-c","-tags=integration,load","./pgbase","-o",str(temporary/f"{side}.test")],cwd=work,env=env,check=True)
        metadata["phase"]="measurement"
        write_metadata()
        ledger=[]
        for index,side in enumerate(metadata["order"],1):
            name=f"{index:02d}-{side}-mixed"
            command=[str(temporary/f"{side}.test"),"-test.run=^TestMixedHTTPPoolQualification$","-test.count=1","-test.v","-test.timeout=10m",
                     f"-test.cpuprofile={output/name}.cpu",f"-test.memprofile={output/name}.mem"]
            record={"index":index,"side":side,"kind":"mixed","name":name,"command":command,"started":time.time()}
            print("START",name,flush=True)
            with (output/f"{name}.log").open("w") as log:
                process=subprocess.Popen(command,cwd=temporary/side,env=env,stdout=log,stderr=subprocess.STDOUT)
                _,status,usage=os.wait4(process.pid,0)
                code=os.waitstatus_to_exitcode(status)
                process.returncode=code
            record.update(exit_code=code,elapsed_seconds=time.time()-record["started"],user_seconds=usage.ru_utime,system_seconds=usage.ru_stime,
                          maxrss_kib=usage.ru_maxrss,voluntary_context_switches=usage.ru_nvcsw,involuntary_context_switches=usage.ru_nivcsw)
            ledger.append(record)
            (output/"ledger.json").write_text(json.dumps(ledger,indent=2)+"\n")
            print("END",name,"exit",code,"seconds",round(record["elapsed_seconds"],3),flush=True)
            if code:
                raise RuntimeError(f"{name} failed; preserve its log and do not silently retry or omit it")
        metadata["phase"]="analysis"
        write_metadata()
        subprocess.run(["python3",str(repo/"scripts/analyze_repeated_load.py"),str(output),str(output/"analysis"),"--http-only","--http-cycles",str(args.cycles)],cwd=repo,check=True)
        metadata["phase"]="complete"
    except BaseException as error:
        metadata["failure"]={"type":type(error).__name__,"message":str(error)}
        raise
    finally:
        metadata["ended"]=time.time()
        write_metadata()
        hashes={str(path.relative_to(output)):hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(output.rglob("*")) if path.is_file()}
        (output/"manifest.json").write_text(json.dumps(hashes,indent=2)+"\n")
        for work in worktrees:
            subprocess.run(["git","worktree","remove","--force",str(work)],cwd=repo,check=False)


if __name__=="__main__":
    main()
