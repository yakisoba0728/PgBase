#!/usr/bin/env python3
"""Regenerate the 2026-09-11 whole-codebase audit publication.

Inputs remain outside this reporting worktree so the fixed-head worker evidence
is never rewritten by publication.  Run from any directory:

  python3 docs/audits/2026-09-11/build_audit_report.py \
    --input-root /absolute/path/to/.local/full-audit-2026-09-11
"""
from __future__ import annotations

import argparse, json, re
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any

OUT = Path(__file__).resolve().parent
REPO = OUT.parents[2]
GROUPS = ("schema_fields_migrations", "records_query_rules", "auth_mail", "files_realtime", "extensions", "operations", "sdk", "ui", "repository_delivery", "documentation")
ALIASES = {"UI-001": "RQR-001"}
OVERRIDES = {"SCH-07": ("defect", "RQR-003"), "UI-07": ("defect", "RQR-003")}
PATH_RE = re.compile(r"(?<![\w/])((?:internal|pgbase|sdk|ui|migrations|cmd|tests)/[A-Za-z0-9_./-]+\.(?:go|ts|tsx|mjs|dart|json|sql|sh|md|yml|yaml))(?:\:(\d+)(?:-\d+)?)?")

def load(path: Path) -> Any:
    with path.open(encoding="utf-8") as f: return json.load(f)

def arr(path: Path, key: str) -> list[dict[str, Any]]:
    value = load(path)
    rows = value if isinstance(value, list) else value.get(key) if isinstance(value, dict) else None
    if not isinstance(rows, list) or not all(isinstance(x, dict) for x in rows): raise ValueError(f"invalid {path}")
    return rows

def items(x: Any) -> list[Any]: return [] if x in (None, "") else x if isinstance(x, list) else [x]

def norm(x: Any) -> Any:
    if isinstance(x, list): return [norm(v) for v in x]
    if isinstance(x, dict): return {k:norm(v) for k,v in x.items()}
    if not isinstance(x, str): return x
    x=re.sub(r"/Users/[^/]+/Documents/GitHub/PgBase/", "", x)
    x=x.replace("/Users/", "<absolute-checkout-root>/")
    return re.sub(r"/(?:private/)?tmp/[^/]+/", "", x)

def write_json(path: Path, value: Any) -> None:
    # Worker notes can quote a historical absolute path literally.  Do not
    # publish a workstation path merely because it occurred in quoted evidence.
    rendered=json.dumps(value,ensure_ascii=False,indent=2)+"\n"
    rendered=re.sub(r"/Users/[^/\"\s]+/Documents/GitHub/PgBase/", "", rendered)
    rendered=re.sub(r"/Users/[^\"\s]*", "<absolute-checkout-root>", rendered)
    rendered=rendered.replace("/Users/", "<absolute-checkout-root>/")
    rendered=re.sub(r"/(?:private/)?tmp/[^/\"\s]+/", "", rendered)
    path.write_text(rendered,encoding="utf-8")

def text(x: Any) -> list[str]:
    out=[]
    for v in items(x):
        if isinstance(v, str): out.append(norm(v))
        elif isinstance(v, dict):
            parts=[v.get(k) for k in ("path","file","lines","range","reason","observation","command","result")]
            out.append(" — ".join(str(norm(p)) for p in parts if p not in (None,"")))
        else: out.append(str(v))
    return [v for v in out if v]

def short(s: str, n=160) -> str:
    s=" ".join(s.split()); return s if len(s)<=n else s[:n-1].rstrip()+"…"

def compact(x: Any, n=2, width=160) -> list[str]:
    r=[short(v,width) for v in text(x)]
    return r[:n]+([f"그 외 {len(r)-n}건"] if len(r)>n else [])

def esc(x: Any) -> str: return str(x or "—").replace("|","\\|").replace("\n","<br>")

def link(s: str) -> str:
    def sub(m: re.Match[str]) -> str:
        path,line=m.groups(); label=path+(f":{line}" if line else "")
        if not (REPO/path).exists():
            return f"`{label}` (보존된 격리 증거; 출판 경로 없음)"
        return f"[{label}](../../../{path}{'#L'+line if line else ''})"
    return PATH_RE.sub(sub,esc(s))

def anchor(i: str) -> str: return "finding-"+i.lower()

def source_features(inp: Path) -> tuple[dict[str,dict[str,Any]],dict[str,str]]:
    rows, owner={},{}
    for group in GROUPS:
        p=inp/group/"features.json"
        if not p.exists(): continue
        for r in arr(p,"features"):
            if not isinstance(r.get("id"),str) or r["id"] in rows: raise ValueError(f"bad feature {p}")
            rows[r["id"]]=norm(r); owner[r["id"]]=group
    fin=inp/"records_query_rules/features-finalization.json"
    if fin.exists():
        for r in load(fin).get("entries",[]):
            if r["id"] not in rows: raise ValueError("unknown finalization id")
            rows[r["id"]]["verdict"]=r["verdict"]
            rows[r["id"]]["finalizationBasis"]=norm(r.get("basis"))
            rows[r["id"]]["finalizationLimitation"]=norm(r.get("limitation"))
    for fid,(verdict,finding) in OVERRIDES.items():
        rows[fid]["verdict"]=verdict; rows[fid]["findings"]=sorted(set(items(rows[fid].get("findings"))+[finding]))
    return rows,owner

def source_findings(inp: Path) -> list[dict[str,Any]]:
    raw={}
    for group in GROUPS:
        p=inp/group/"findings.json"
        if p.exists():
            for r in arr(p,"findings"): raw[r["id"]]=norm({**r,"sourceGroup":group})
    independent={}
    for name in ("platform-findings.json","data-findings.json","root-sdk-findings.json","truncate-findings.json"):
        for r in arr(inp/"cross-validation"/name,"findings"):
            independent[r["id"]]=norm({**r,"adjudicationSource":"cross-validation/"+name})
    out=[]
    for fid in sorted(set(raw)|set(independent)):
        r=dict(raw.get(fid,{})); ind=independent.get(fid,{}) ; r.update(ind); r["id"]=fid
        if ind.get("severity"): r["severity"]=ind["severity"]
        elif ind.get("priority"): r["severity"]=ind["priority"]
        elif "severity" not in r: r["severity"]=r.get("priority","P?")
        if r.get("verdict")=="reject": r.update(disposition="intentional_difference",countedAsProductDefect=False)
        elif fid in ALIASES: r.update(disposition="impact_alias",aliasOf=ALIASES[fid],countedAsProductDefect=False)
        elif fid.startswith("DOC-"): r.update(disposition="documentation",countedAsProductDefect=False,countedAsDocumentationFinding=True)
        else: r.update(disposition="confirmed" if r.get("verdict")=="confirm" else "worker_reported",countedAsProductDefect=True)
        out.append(r)
    return out

def frefs(ids: list[str], by: dict[str,dict[str,Any]]) -> str:
    a=[]
    for i in ids:
        f=by.get(i); suffix=" 영향" if f and f.get("disposition")=="impact_alias" else ""
        a.append(f"[{i} · {f.get('severity','P?')}{suffix}](#{anchor(i)})" if f else esc(i))
    return "<br>".join(a) or "—"

def verdict(row: dict[str,Any], by: dict[str,dict[str,Any]]) -> str:
    name={"defect":"결함 확인","contract_gap":"계약 누락","intentional_difference":"명시 차이","verified":"검토 범위 내 결함 미발견","static_reviewed":"검토 범위 내 결함 미발견","unverified":"미검증","not_verified":"미검증","implemented_partial":"미검증 (부분 검토)"}.get(row["verdict"],"미검증")
    refs=frefs(row["findingIds"],by); return name if refs=="—" else name+"<br>"+refs

def coverage(inp: Path) -> dict[str,Any]:
    union=load(inp/"coverage-union.json"); entries=[]
    for row in union["entries"]:
        if not row.get("complete"): raise ValueError(f"incomplete coverage {row['path']}")
        candidates=[e for e in row["evidence"] if e.get("method") in ("full_read","source_provenance") and e.get("notes")]
        if not candidates: raise ValueError(f"no normalized evidence {row['path']}")
        e=next((x for x in candidates if x.get("method")=="full_read"),candidates[0])
        entries.append({"path":row["path"],"blobSha":row["expectedBlobSha"],"readRanges":e.get("ranges",[]),"method":e["method"],"notes":norm(e["notes"]),"kind":row["kind"],"group":row["group"],"provenance":{"coverageReport":Path(e["report"]).name,"batch":e.get("batch")}})
    if len(entries)!=1385 or len({e["path"] for e in entries})!=1385: raise ValueError("coverage path contract")
    methods=Counter(e["method"] for e in entries)
    return {"schemaVersion":1,"baseSha":union["baseSha"],"assignedFileCount":1385,"crossReadsExcluded":True,"methodCounts":dict(methods),"entries":entries}

def main(inp: Path) -> None:
    comparison_path=REPO/"docs/pocketbase-feature-comparison-current-2026-09-10.json"; comparison=load(comparison_path); base=load(inp/"state.json")["baseSha"]
    source,owners=source_features(inp); ids=[r["id"] for r in comparison["rows"]]
    if len(ids)!=232 or len(set(ids))!=232 or set(ids)!=set(source): raise ValueError("232 feature ID contract")
    findings=source_findings(inp); by={f["id"]:f for f in findings}; related=defaultdict(list)
    for f in findings:
        for fid in items(f.get("featureIds")):
            if fid in source: related[fid].append(f["id"])
    # An independently rejected finding is retained in findings.json as a
    # rationale, never as an active feature defect.  Apply the rule generally
    # so a future worker refresh cannot reintroduce AUTH-31's stale verdict.
    rejected={f["id"] for f in findings if f.get("disposition")=="intentional_difference"}
    for fid,w in source.items():
        candidate=set(items(w.get("findings"))+related[fid])
        if w.get("verdict") in {"defect","contract_gap"} and candidate and candidate <= rejected:
            w["withdrawnFindingIds"]=sorted(candidate)
            w["findings"]=[]
            if fid=="AUTH-31":
                w["verdict"]="static_reviewed"
                w["limitations"]=list(items(w.get("limitations")))+["Global UNIQUE(key_version) DB constraint and purpose-policy write path make the proposed shared-key trigger impossible; static source/assertion review only."]
            else:
                w["verdict"]="static_reviewed"
                w["limitations"]=list(items(w.get("limitations")))+["The only linked finding was independently rejected; no active defect is retained in this review scope."]
    rows=[]
    for old in comparison["rows"]:
        fid=old["id"]; w=source[fid]; fids=sorted(set(items(w.get("findings"))+related[fid]))
        if w.get("withdrawnFindingIds"):
            fids=[i for i in fids if i not in set(w["withdrawnFindingIds"])]
        lim=text(w.get("limitations"))
        if w.get("finalizationBasis"): lim.append("최종화 근거: "+w["finalizationBasis"])
        if w.get("finalizationLimitation"): lim.append("최종화 한계: "+w["finalizationLimitation"])
        rows.append({"ID":fid,"분야":old.get("domain") or old.get("category"),"기능":old.get("feature"),"verdict":w.get("verdict","unverified"),"workerGroup":owners[fid],"실제trace":text(w.get("implementationTrace")),"PocketBase근거":text(w.get("pbEvidence")),"findingIds":fids,"withdrawnFindingIds":w.get("withdrawnFindingIds",[]),"읽은테스트및실행증거":["read: "+x for x in text(w.get("testsRead"))]+["run: "+x for x in text(w.get("testsRun"))],"differences":text(w.get("differences")),"limitations":lim,"코드참조":text(w.get("implementationTrace"))})
    for row in rows:
        active=[by[i] for i in row["findingIds"] if i in by]
        if row["verdict"] in {"defect","contract_gap"} and active and all(f.get("disposition")=="intentional_difference" for f in active):
            raise ValueError(f"reject-only active verdict: {row['ID']}")
    cov=coverage(inp); validation=load(inp/"coverage-validation.json")
    if validation["counts"]!={"assigned":1385,"observedAssigned":1385,"missing":0,"blobsBad":0,"assignmentBlobsBad":0,"incomplete":0}: raise ValueError("coverage validation not final")
    products=[f for f in findings if f.get("countedAsProductDefect")]; docs=[f for f in findings if f.get("countedAsDocumentationFinding")]
    pc=Counter(f["severity"] for f in products)
    if len(products)!=20 or pc!={"P1":9,"P2":11} or len(docs)!=2 or any(f["severity"]!="P3" for f in docs): raise ValueError("finding count contract")
    features={"schemaVersion":1,"kind":"consolidated-audit","status":"final","baseSha":base,"featureIdContract":{"expected":232,"observed":232,"exact":True},"verdictCounts":dict(Counter(r["verdict"] for r in rows)),"rows":rows}
    OUT.mkdir(parents=True,exist_ok=True); write_json(OUT/"features.json",features)
    write_json(OUT/"findings.json",{"schemaVersion":1,"baseSha":base,"productFindingCounts":dict(pc),"documentationFindingCounts":dict(Counter(f["severity"] for f in docs)),"findings":findings})
    write_json(OUT/"coverage.json",cov)
    scope={"schemaVersion":1,"baseSha":base,"pocketBaseSha":load(inp/"state.json")["pocketbaseSha"],"phases":load(inp/"state.json")["phases"],"featureRows":232,"coverage":{"assigned":1385,"complete":1385,"crossReadsExcluded":True,"validator":"validate_audit_coverage.py"},"limits":["Coverage validates declared fixed-head path/blob/range evidence; it cannot prove what a reviewer read.","verified and static_reviewed mean no defect found in their recorded scope, not that every runtime path ran."],"inputArtifacts":["charter.md","state.json","assignments.json","coverage-validation.json","group features/findings/coverage/report","cross-validation platform/data/root-sdk/truncate findings"]}
    write_json(OUT/"scope.json",scope)
    md=["# PgBase 전체 코드 감사 — 기능 표","",f"기준 SHA: `{base}`. 232개 원래 ID와 1:1 대응한다. `검토 범위 내 결함 미발견`은 기록된 검토 범위의 결과이며 모든 경로 실행을 뜻하지 않는다.","","| ID | 분야 | 기능 | 감사판정 | 실제 trace | 발견 ID | 읽은 테스트·실행 증거 | 명시 차이·한계 | 코드 참조 |","| --- | --- | --- | --- | --- | --- | --- | --- | --- |"]
    for r in rows:
        trace="<br>".join("• "+link(x) for x in compact(r["실제trace"],2,135)); pb=compact(r["PocketBase근거"],1,110)
        if pb: trace+=("<br>• PB: "+esc(pb[0]))
        reads=[x[6:] for x in r["읽은테스트및실행증거"] if x.startswith("read: ")]; runs=[x[5:] for x in r["읽은테스트및실행증거"] if x.startswith("run: ")]
        tests=("읽음: "+"; ".join(link(x) for x in compact(reads,2,110)) if reads else "")+("<br>실행: "+"; ".join(link(x) for x in compact(runs,1,145)) if runs else "")
        limits=["차이: "+esc(x) for x in compact(r["differences"],1,135)]+["한계: "+esc(x) for x in compact(r["limitations"],2,135)]
        refs="<br>".join(link(x) for x in compact(r["코드참조"],3,135))
        md.append("| "+" | ".join([esc(r["ID"]),esc(r["분야"]),esc(r["기능"]),verdict(r,by),trace or "—",frefs(r["findingIds"],by),tests or "실행 증거 미기록","<br>".join(limits) or "—",refs or "—"])+" |")
    md += ["","## 발견 상세",""]
    for f in findings:
        md += [f"### <a id=\"{anchor(f['id'])}\"></a>{f['id']} · {f['severity']} · {esc(f.get('title'))}","",f"상태: `{f['disposition']}`. 관련 기능: {', '.join(items(f.get('featureIds'))) or '별도 문서/교차검증 항목'}.",""]
        detail=f.get("conclusion") or f.get("observed") or f.get("impact")
        if detail: md += [esc(detail),""]
        ev=compact(f.get("evidence"),8,220)
        if ev: md += ["근거:"]+["- "+link(x) for x in ev]+[""]
    (OUT/"features.md").write_text("\n".join(md).rstrip()+"\n",encoding="utf-8")
    raw_counts=Counter(r["verdict"] for r in rows)
    raw_label=", ".join(f"{k} {raw_counts[k]}" for k in sorted(raw_counts))
    korean={
        "EXT-AUD-005":("extension object 덮어쓰기 실패가 기존 blob을 지움","기존 metadata가 없는 object를 가리켜 확장 상태가 사라진다.","새 blob을 확정하기 전 metadata 성공을 보장하고 이전 blob을 보존한다."),
        "SFM-001":("migration snapshot이 호환 이름·검증 모드를 유실함","재생성 migration이 PocketBase 호환 조회와 입력 검증 의미를 바꿀 수 있다.","snapshot/replay에 CompatibilityName·ValidationMode를 포함한다."),
        "SFM-002":("migration snapshot이 고급 index 의미를 평탄화함","partial·expression index가 일반 index로 바뀌어 제약과 계획이 달라질 수 있다.","index의 keys·where·raw SQL을 snapshot에 보존한다."),
        "OPS-F-001":("runtime settings 반환값이 rate-limit slice를 공유함","호출자가 검증·저장 없이 live rate-limit 설정을 바꾸거나 경합을 만들 수 있다.","모든 Get·Update·Reload 경계에서 slice를 깊게 복사한다."),
        "RQR-003":("다중 관계 collection truncate가 잘못된 schema의 edge table을 조회함","일반 DDL의 다중 관계 source·target collection은 비어 있지 않으면 truncate할 수 없다.","관계 개수 조회를 pgbase_relation schema로 고정한다."),
        "RQR-001":("유효한 PocketBase 관계 규칙을 저장할 수 없음","관리자가 호환 API로 관계 규칙을 발행하지 못한다.","저장 단계에서 PocketBase catalog compiler를 선택한다."),
        "RQR-002":("역방향 관계 filter가 잘못된 join 조건을 생성함","역관계 조회가 SQL 오류 또는 다른 행을 반환할 수 있다.","reverse hop의 단일·다중 relation predicate를 분기한다."),
        "EXT-AUD-002":("확장 OTP·인증 알림 helper가 caller payload를 버림","확장 코드가 만든 수신자·본문 정보가 전송 경로에 반영되지 않는다.","검증한 payload를 실제 mail helper까지 전달한다."),
        "EXT-AUD-001":("확장 ApiError가 항상 정리된 500으로 평탄화됨","호출자가 원래 status와 error data를 받아 분기할 수 없다.","호환 ApiError의 status·data를 보존해 응답한다."),
        "AUTHMAIL-001":("email 없는 OAuth 가입이 첫 계정만 만들고 다음 계정을 거절함","동일 realm의 서로 다른 provider subject가 불일치하게 가입 실패한다.","빈 email을 일관되게 거절하거나 별도 identity key로 저장한다."),
        "AUTHMAIL-002":("존재하지 않는 auth identity field를 정책으로 저장함","클라이언트에 보이지만 어떤 사용자도 만족할 수 없는 로그인 방법이 생긴다.","realm schema와 unique 조건을 정책 저장 전에 검증한다."),
        "EXT-AUD-003":("$app.runInTransaction이 transaction-bound app과 중첩을 제공하지 않음","호환 extension transaction 안에서 model 동작과 nesting이 기대와 달라진다.","transaction-bound app facade와 nesting 정책을 구현한다."),
        "EXT-AUD-004":("일반 record CRUD 실패가 model error hook을 내보내지 않음","확장 hook이 실패 lifecycle을 관찰하거나 보완하지 못한다.","일반 persistence 오류에서도 선언한 error phase를 emit한다."),
        "EXT-AUD-006":("stream·multipart·copy object write가 object.write hook을 우회함","확장 검증·감사 hook이 일부 쓰기 경로에서 실행되지 않는다.","모든 public write entry point를 hook pipeline으로 통합한다."),
        "EXT-AUD-007":("object prefix의 %와 _가 SQL wildcard로 해석됨","list와 deletePrefix가 같은 namespace의 무관한 object를 선택할 수 있다.","LIKE escape 또는 prefix-safe comparison을 사용한다."),
        "EXT-AUD-008":("생성 JavaScript declaration이 호환 API·byte type을 누락함","유효한 확장 코드가 editor에서 거절되고 잘못된 타입 가정을 허용한다.","runtime surface에서 declaration을 생성·검증한다."),
        "OPS-F-002":("managed restore가 open 실패 뒤 provisioned target을 폐기하지 않음","재시도할 때 복원된 DB·file target이 누적될 수 있다.","target 소유 직후 cleanup defer를 등록한다."),
        "SDK-AUDIT-001":("TypeScript realtime이 terminal error도 무한 재시도함","권한·cursor 오류에서 subscription이 완료되지 않고 요청을 반복한다.","terminal PgBaseError를 종료하고 caller에 전달한다."),
        "SDK-AUDIT-003":("Dart realm OAuth browser helper가 기본 realm으로 시작함","선택 realm의 provider 설정 대신 기본 realm 흐름으로 진입할 수 있다.","realm-specific OAuth start helper를 사용한다."),
        "SFM-003":("native name과 compatibility name이 대소문자 무시 충돌 가능","관리자 collection 조회가 map 순서에 따라 다른 schema를 반환할 수 있다.","두 이름 공간을 함께 case-fold uniqueness 검증한다."),
        "DOC-001":("비교표 source pointer가 workstation 절대 경로를 사용함","다른 checkout의 검토자가 증거를 열 수 없다.","tracked source pointer를 repo-relative path로 유지한다."),
        "DOC-RT-001":("현재 비교표가 realtime handshake 부재를 잘못 기록함","현재 기능과 문서 상태가 어긋난다.","PB_CONNECT 구현과 확인 근거로 비교표를 정정한다."),
    }
    ordered_p1=["EXT-AUD-005","SFM-001","SFM-002","OPS-F-001","RQR-003","RQR-001","RQR-002","EXT-AUD-002","EXT-AUD-001"]
    product_by={f["id"]:f for f in products}; doc_by={f["id"]:f for f in docs}
    ordered=[product_by[i] for i in ordered_p1]+sorted((f for f in products if f["severity"]=="P2"),key=lambda f:f["id"])+sorted(docs,key=lambda f:f["id"])
    phase_rows=[("1","schema·fields·migrations·records·query/rules·auth/mail","128","기능별 구현 trace·검증·권한·storage 경로 읽기"),("2","files·realtime·extensions·operations","60","파일·확장·운영 lifecycle과 rollback·cleanup 경계 읽기"),("3","SDK·UI·repository delivery","44","client/UI/repository surface와 기존 assertion 읽기"),("4","교차검증·coverage closure·통합 보고","—","독립 finding 우선 판정과 ID·blob·range 합집합 검증")]
    group_rows=[("schema_fields_migrations",224),("records_query_rules",184),("auth_mail",136),("files_realtime",55),("extensions",99),("operations",248),("sdk",48),("ui",135),("repository_delivery",138),("documentation",118)]
    report=["# PgBase 전체 코드 감사 — 2026-09-11","",f"기준 SHA는 `{base}`이다. 1,385개 tracked path의 고정 HEAD blob·범위 증거와 232개 기존 기능 ID를 전수 대조했다. 기능 수용 표의 과거 `구현 232/부분 0/미구현 0`은 수용 기록으로 보존하지만, 현재 품질 판단은 이 독립 감사의 20개 제품 결함과 2개 문서 결함이다.","","## 핵심 결과","",f"- 제품 결함: **20건** — P1 **{pc['P1']}**, P2 **{pc['P2']}**.","- 문서 결함: **2건**, 모두 P3. 제품 결함 집계와 분리했다.",f"- coverage: 1,385/1,385 path가 complete evidence를 가졌고 missing·incomplete·blob mismatch는 0이다. `full_read` **{cov['methodCounts'].get('full_read',0)}**, `source_provenance` **{cov['methodCounts'].get('source_provenance',0)}**이다.",f"- 기능 행 raw 판정(232개, finding 중복 제거 전): {raw_label}. unique 제품 finding 집계는 feature 행 수가 아니라 finding ID로 계산했다.","","## 4단계 수행 범위","","| Phase | 범위 | 기능 행 | 수행 내용 |","| --- | --- | ---: | --- |"]
    report += [f"| {a} | {b} | {c} | {d} |" for a,b,c,d in phase_rows]
    report += ["","## 10개 그룹 파일 전수 완료","","| 그룹 | assigned path | 전수 증거 |","| --- | ---: | --- |"]
    report += [f"| `{g}` | {n} | fixed-head blob·전체 read range 또는 허용 provenance |" for g,n in group_rows]
    report += ["","`full_read`는 source를 실제 범위로 읽은 증거이고, `source_provenance`는 generated/config/asset 또는 구조화 생성물의 source·generator·integrity 근거다. 두 방법은 같은 의미로 세지 않으며 cross-read는 coverage 합계에서 제외했다.","","## 우선순위 결함","","| 등급 | ID | 요약 | 영향 | 권장 보완 |","| --- | --- | --- | --- | --- |"]
    for f in ordered:
        title,impact,recommend=korean[f["id"]]
        report.append(f"| {f['severity']} | [{f['id']}](audits/2026-09-11/features.md#{anchor(f['id'])}) | {title} | {impact} | {recommend} |")
    report += ["","## 선택 실행과 실제 재현","","RQR-001·RQR-002의 격리 integration 재현은 결함을 확인했고, RQR-003은 다중 relation의 source/target 모두에서 expected-success truncate가 `42P01`로 실패하는 것을 재현했다. 반대로 기존 scalar `TestTruncate*`는 통과했지만 해당 relation branch를 다루지 않았다.","","SFM-001·SFM-002는 PostgreSQL migration snapshot의 export→replay→store 경로를 끝까지 대조해 호환 이름·검증 모드와 고급 index 정의가 실제 replay 입력에서 빠지는 효과를 확인했다. SFM-002는 이 교차검증에서 물리 index를 새로 변경하지 않았으므로 그 한계를 유지한다.","","FIL-RT-001은 invalid multipart 요청 뒤 ready row·object가 `1 → 1`로 보존되고 새 part가 정리되는 focused 검증으로 철회했다. FIL-RT-002도 durable deletion queue와 `DrainOne`이 metadata 삭제 뒤 physical object를 제거하는 격리 검증으로 철회했다. 환경 파일, 자격정보, 미실행 TOTP 테스트와 임시 DB 식별자는 출판물에서 제외했다.","","## 판정과 한계","","`verified`와 `static_reviewed`는 읽은 source/assertion 및 기록된 실행 범위에서 결함을 찾지 못했다는 뜻이다. 모든 경로가 동적으로 실행됐다는 뜻은 아니다. 정적 trace, 선택된 focused test, 기존 assertion 읽기를 구분했으며 외부 OAuth/SMTP/production 서비스는 사용하지 않았다.","","RQR-003은 정상 DDL로 만든 다중 relation에 참여하는 비어 있지 않은 collection의 truncate에만 한정했다. SCH-07과 UI-07에 반영했지만 일반 REC-05 delete나 FLD-12 relation 전체로 확장하지 않았다. UI-001은 RQR-001의 UI 영향 alias라 unique 결함으로 중복 집계하지 않았다.","","FIL-RT-001, FIL-RT-002, AUTHMAIL-003, SDK-AUDIT-002는 철회 또는 명시 차이로 남기고 제품 결함에서 제외했다. SFM-001은 compatibilityName/validationMode 손실 범위로 좁혔으며 update Type/System 보존을 손실로 보고하지 않았다.","","## 전체 증거","","- [232개 기능 표](audits/2026-09-11/features.md)","- [구조화 기능 데이터](audits/2026-09-11/features.json)","- [구조화 finding 데이터](audits/2026-09-11/findings.json)","- [1,385개 coverage 증거](audits/2026-09-11/coverage.json)","- [범위와 방법](audits/2026-09-11/scope.json)",""]
    (REPO/"docs/codebase-audit-2026-09-11.md").write_text("\n".join(report),encoding="utf-8")
    comparison["independentAudit"]={"date":"2026-09-11","baseSha":base,"report":"docs/codebase-audit-2026-09-11.md","features":"docs/audits/2026-09-11/features.md","productFindings":20,"documentationFindings":2,"note":"Existing status fields are historical feature-acceptance records and are not independent audit verdicts."}
    # This historical table is preserved byte-for-byte except for this added
    # metadata object; unlike newly generated audit files, do not sanitize or
    # rewrite its established feature rows.
    comparison_path.write_text(json.dumps(comparison,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")

if __name__ == "__main__":
    p=argparse.ArgumentParser(); p.add_argument("--input-root",type=Path,required=True); main(p.parse_args().input_root.resolve())
