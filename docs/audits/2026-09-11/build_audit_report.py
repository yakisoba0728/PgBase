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
    rows=[]
    for old in comparison["rows"]:
        fid=old["id"]; w=source[fid]; fids=sorted(set(items(w.get("findings"))+related[fid])); lim=text(w.get("limitations"))
        if w.get("finalizationBasis"): lim.append("최종화 근거: "+w["finalizationBasis"])
        if w.get("finalizationLimitation"): lim.append("최종화 한계: "+w["finalizationLimitation"])
        rows.append({"ID":fid,"분야":old.get("domain") or old.get("category"),"기능":old.get("feature"),"verdict":w.get("verdict","unverified"),"workerGroup":owners[fid],"실제trace":text(w.get("implementationTrace")),"PocketBase근거":text(w.get("pbEvidence")),"findingIds":fids,"읽은테스트및실행증거":["read: "+x for x in text(w.get("testsRead"))]+["run: "+x for x in text(w.get("testsRun"))],"differences":text(w.get("differences")),"limitations":lim,"코드참조":text(w.get("implementationTrace"))})
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
    report=["# PgBase 전체 코드 감사 — 2026-09-11","",f"기준 SHA는 `{base}`이다. 1,385개 tracked path의 고정 HEAD blob·범위 증거와 232개 기존 기능 ID를 전수 대조했다. 기능 수용 표의 과거 `구현 232/부분 0/미구현 0`은 수용 기록으로 보존하지만, 현재 품질 판단은 이 독립 감사의 20개 제품 결함과 2개 문서 결함이다.","","## 핵심 결과","",f"- 제품 결함: **20건** — P1 **{pc['P1']}**, P2 **{pc['P2']}**.","- 문서 결함: **2건**, 모두 P3. 제품 결함 집계와 분리했다.","- 범위: schema/fields/migrations·records/query/rules·auth/mail·files/realtime/extensions/operations·SDK/UI/repository delivery/documentation의 4 phase에서 그룹별 assigned path를 전수 읽기 증거로 대조했다.",f"- coverage: 1,385/1,385 path가 complete evidence를 가졌고 missing·incomplete·blob mismatch는 0이다. `full_read` **{cov['methodCounts'].get('full_read',0)}**, `source_provenance` **{cov['methodCounts'].get('source_provenance',0)}**이다.",f"- 기능 행 raw 판정(232개, finding 중복 제거 전): {raw_label}. unique 제품 finding 집계는 feature 행 수가 아니라 finding ID로 계산했다.","","## 우선순위 결함","","| 등급 | ID | 요약 |","| --- | --- | --- |"]
    for f in products+docs: report.append(f"| {f['severity']} | [{f['id']}](audits/2026-09-11/features.md#{anchor(f['id'])}) | {esc(f.get('title'))} |")
    report += ["","## 선택 실행과 실제 재현","","기존 선택 테스트와 focused 재현을 구분해 읽었다. RQR-001·RQR-002의 격리 integration 재현은 결함을 확인했고, RQR-003은 다중 relation의 source/target 모두에서 expected-success truncate가 `42P01`로 실패하는 것을 재현했다. 반대로 기존 scalar `TestTruncate*`는 통과했지만 해당 relation branch를 다루지 않았다. 환경 파일, 자격정보, 미실행 TOTP 테스트와 임시 DB 식별자는 출판물에서 제외했다.","","## 판정과 한계","","`verified`와 `static_reviewed`는 읽은 source/assertion 및 기록된 실행 범위에서 결함을 찾지 못했다는 뜻이다. 모든 경로가 동적으로 실행됐다는 뜻은 아니다. 정적 trace, 선택된 focused test, 기존 assertion 읽기를 구분했으며 외부 OAuth/SMTP/production 서비스는 사용하지 않았다.","","RQR-003은 정상 DDL로 만든 다중 relation에 참여하는 비어 있지 않은 collection의 truncate에만 한정했다. SCH-07과 UI-07에 반영했지만 일반 REC-05 delete나 FLD-12 relation 전체로 확장하지 않았다. UI-001은 RQR-001의 UI 영향 alias라 unique 결함으로 중복 집계하지 않았다.","","FIL-RT-001, FIL-RT-002, AUTHMAIL-003, SDK-AUDIT-002는 철회 또는 명시 차이로 남기고 제품 결함에서 제외했다. SFM-001은 compatibilityName/validationMode 손실 범위로 좁혔으며 update Type/System 보존을 손실로 보고하지 않았다.","","## 전체 증거","","- [232개 기능 표](audits/2026-09-11/features.md)","- [구조화 기능 데이터](audits/2026-09-11/features.json)","- [구조화 finding 데이터](audits/2026-09-11/findings.json)","- [1,385개 coverage 증거](audits/2026-09-11/coverage.json)","- [범위와 방법](audits/2026-09-11/scope.json)",""]
    (REPO/"docs/codebase-audit-2026-09-11.md").write_text("\n".join(report),encoding="utf-8")
    comparison["independentAudit"]={"date":"2026-09-11","baseSha":base,"report":"docs/codebase-audit-2026-09-11.md","features":"docs/audits/2026-09-11/features.md","productFindings":20,"documentationFindings":2,"note":"Existing status fields are historical feature-acceptance records and are not independent audit verdicts."}
    # This historical table is preserved byte-for-byte except for this added
    # metadata object; unlike newly generated audit files, do not sanitize or
    # rewrite its established feature rows.
    comparison_path.write_text(json.dumps(comparison,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")

if __name__ == "__main__":
    p=argparse.ArgumentParser(); p.add_argument("--input-root",type=Path,required=True); main(p.parse_args().input_root.resolve())
