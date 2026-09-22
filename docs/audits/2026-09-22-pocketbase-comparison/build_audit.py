import collections, hashlib, json, re, subprocess, sys
from pathlib import Path
sys.stdout.reconfigure(encoding="utf-8")
root=Path(__file__).resolve().parents[3]
out=root/"docs/audits/2026-09-22-pocketbase-comparison"
names=["schema-fields","records-query","auth-rules-mail","files-realtime","extensions-migrations","sdk","ui","operations"]
labels={"equivalent":"좁은 계약 일치","adapted":"대응 구현·설계 차이","partial":"보완 필요","missing":"미구현","unverified":"판정 보류"}
historical=json.loads((root/"docs/pocketbase-feature-comparison-current-2026-09-11.json").read_text(encoding="utf-8-sig"))["rows"]
old={r["id"]:r for r in historical}
rows=[]
sources={}
for name in names:
    part=json.loads((out/(name+".json")).read_text(encoding="utf-8-sig"))
    for item in part:
        item=dict(item)
        item["auditDomainFile"]=name+".md"
        item["historicalBaseline"]=item["id"] in old
        if item["id"] in old:
            item["historicalStatus"]=old[item["id"]]["status"]
        rows.append(item)
    sources[name]=part
errors=[]
ids=collections.Counter(r["id"] for r in rows)
for key,n in ids.items():
    if n!=1:errors.append(f"duplicate {key}: {n}")
for key in old:
    if key not in ids:errors.append(f"missing historical {key}")
required=["id","category","feature","status","pbContract","pbSource","pgImplementation","pgSource","testSource","verification","gap","priority"]
def aslist(v):return v if isinstance(v,list) else [v]
pins={"pocketbase":"5cec579da984436a258602a46a96302fbd31f77c","js-sdk":"01efa38294141f71bc60dc6359a06c29e19b93fb","dart-sdk":"b3f0831c3bbb2257438fd77041c09f2826d316be"}
cited=set()
def contentlines(p):
    try:return p.read_text(encoding="utf-8-sig").splitlines()
    except UnicodeDecodeError:return p.read_text(encoding="utf-16").splitlines()
for r in rows:
    for k in required:
        if k not in r:errors.append(f'{r["id"]}: missing {k}')
    if r["status"] not in labels:errors.append(f'{r["id"]}: bad status {r["status"]}')
    if r["verification"] not in ["executed-now","prior-run","test-exists","static-only"]:errors.append(f'{r["id"]}: bad verification')
    for url in aslist(r["pbSource"])+[ref for ref in aslist(r["testSource"]) if ref.startswith("https://")]:
        m=re.fullmatch(r"https://github.com/pocketbase/(pocketbase|js-sdk|dart-sdk)/blob/([a-f0-9]{40})/([^#]+)(?:#L(\d+)(?:-L(\d+))?)?",url)
        if not m:
            errors.append(f'{r["id"]}: invalid official URL {url}');continue
        repo,sha,path,line,end=m.groups()
        if pins[repo]!=sha:errors.append(f'{r["id"]}: unpinned SHA')
        f=root/".local/pocketbase-comparison"/repo/path
        if not f.is_file():errors.append(f'{r["id"]}: no PB source {f}')
        elif line and (int(end or line)>len(contentlines(f)) or int(line)<1):errors.append(f'{r["id"]}: PB line out of range {url}')
        cited.add((repo,path))
    for ref in aslist(r["pgSource"])+aslist(r["testSource"]):
        if ref.startswith("https://"):continue
        m=re.fullmatch(r"(.+?)(?::(\d+)(?:[-–]\d+)?)?",ref)
        if not m:
            errors.append(f'{r["id"]}: unexpected local ref {ref}');continue
        path,line=m.groups()
        f=root/path
        if not f.exists():f=out/path
        if not f.is_file():
            errors.append(f'{r["id"]}: local ref not found {ref}');continue
        if line and (int(line)>len(contentlines(f)) or int(line)<1):errors.append(f'{r["id"]}: local line out of range {ref}')
if errors:
    (out/"validation-errors.txt").write_text("\n".join(errors)+"\n",encoding="utf-8")
    print("\n".join(errors))
    raise SystemExit(1)
# Snapshot all tracked/untracked nonignored files, including new implementation, excluding audit artifacts.
listed=subprocess.check_output(["git","ls-files","--cached","--others","--exclude-standard","-z"],cwd=root).decode("utf-8").split("\0")
files=[]
for path in sorted(set(listed)):
    if not path or path.startswith("docs/audits/2026-09-22-pocketbase-comparison/"):continue
    f=root/path
    if not f.is_file():continue
    b=f.read_bytes()
    files.append({"path":path,"bytes":len(b),"sha256":hashlib.sha256(b).hexdigest()})
digest=hashlib.sha256("".join(f'{x["path"]}\0{x["sha256"]}\n' for x in files).encode()).hexdigest()
manifest={"gitHead":subprocess.check_output(["git","rev-parse","HEAD"],cwd=root,text=True).strip(),"branch":subprocess.check_output(["git","branch","--show-current"],cwd=root,text=True).strip(),"fingerprint":digest,"algorithm":"SHA256 of sorted UTF-8 path + NUL + file SHA256 + LF; audit directory excluded; deleted files omitted; git ls-files --cached --others --exclude-standard","fileCount":len(files),"files":files}
(out/"source-manifest.json").write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
by_category={}
for r in rows:by_category.setdefault(r["category"],collections.Counter())[r["status"]]+=1
baseline=[r for r in rows if r["historicalBaseline"]]
added=[r for r in rows if not r["historicalBaseline"]]
summary={"date":"2026-09-22","pocketbase":{"version":"v0.40.4","commit":pins["pocketbase"]},"jsSdk":{"version":"0.28.1","commit":pins["js-sdk"]},"dartSdk":{"version":"0.25.1","commit":pins["dart-sdk"]},"pgbase":{"gitHead":manifest["gitHead"],"branch":manifest["branch"],"uncommittedImplementationIncluded":True,"snapshotFingerprint":digest},"statusDefinitions":labels,"summary":{"historicalRows":len(baseline),"additionalRows":len(added),"totalRows":len(rows),"historicalCounts":dict(collections.Counter(r["status"] for r in baseline)),"additionalCounts":dict(collections.Counter(r["status"] for r in added)),"totalCounts":dict(collections.Counter(r["status"] for r in rows)),"byCategory":by_category,"verificationCounts":dict(collections.Counter(r["verification"] for r in rows)),"uniqueOfficialFilesCited":len(cited)},"scope":"232 historical functional rows plus specifically identified omissions, not every exported PocketBase API or every possible behavior; counts are not a product completion percentage","rows":rows}
(out/"matrix.json").write_text(json.dumps(summary,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
def local_link(ref):
    if ref.startswith("https://"):return f'[공식 테스트]({ref})'
    m=re.fullmatch(r"(.+?)(?::(\d+)(?:[-–]\d+)?)?",ref)
    if not m:return ref
    path,line=m.groups()
    p=root/path
    if not p.exists():p=out/path
    return f'[{ref}]({p.as_posix()}{":"+line if line else ""})'
parts=["# PocketBase v0.40.4 항목별 전체 대조표","",f"기존 {len(baseline)}행 + 추가 {len(added)}행 = {len(rows)}행. 판정 정의·우선순위·검증 한계는 [종합 보고서](README.md)를 따른다. 중간 fragment 파일은 이 표의 집계에 포함하지 않았다.",""]
for r in rows:
    parts += [f'## {r["id"]} · {r["feature"]}',"",f'**{labels[r["status"]]}** · {r["priority"]} · {r["verification"]} · {"기존 항목" if r["historicalBaseline"] else "추가 확인 항목"}',"",f'PocketBase: {r["pbContract"]}',"",f'PgBase: {r["pgImplementation"]}',"",f'차이/남은 범위: {r["gap"]}',"", "공식 근거: "+" / ".join(f"[소스 {i+1}]({url})" for i,url in enumerate(aslist(r["pbSource"]))),"","PgBase 근거: "+" / ".join(local_link(ref) for ref in aslist(r["pgSource"])),"","테스트/검증 근거: "+" / ".join(local_link(ref) for ref in aslist(r["testSource"])),""]
    for key in ["verificationNotes","verificationDetail"]:
        if r.get(key):parts += [str(r[key]),""]
(out/"matrix.md").write_text("\n".join(parts),encoding="utf-8")
(out/"validation.txt").write_text(f"PASS: {len(baseline)}/232 historical IDs present exactly once; {len(added)} added; {len(rows)} rows.\nPASS: required fields/status/verification enums; pinned official source paths/line ranges; local source/test paths/line ranges.\nOfficial cited file count: {len(cited)} (not a claim all lines were audited).\nSource snapshot: {digest}\n",encoding="utf-8")
print(json.dumps(summary["summary"],ensure_ascii=False,indent=2))
print("snapshot",digest)
