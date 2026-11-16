#!/usr/bin/env python3
"""Read existing qualification evidence only; never execute project tests/scanners."""
import collections
import gzip
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import zipfile

repo = Path(sys.argv[1]).resolve()
out = Path(__file__).resolve().parent
ws = repo / 'docs/workstreams/independent-platform-2026-11-16'
ev = ws / 'evidence/final-integrated'
load = lambda p: json.loads(Path(p).read_text())
digest = lambda b: hashlib.sha256(b).hexdigest()
git = lambda *a: subprocess.check_output(['git', '-C', str(repo), *a], text=True).strip()
head = '4091c3be20ec8db177886a2a85550924fce37ff5'
checkout = 'ba838872d72993abf1953dcafc64ba043f978c06'
focused = '277c18cbb5333ca2a925ccb84628702c29b7fcd6'
pr, jobs, arts, live = (load(out / f'{name}.json') for name in ['pr', 'jobs', 'artifacts', 'live-log-digests'])
jobs, arts = jobs['jobs'], arts['artifacts']
assert pr['head_sha'] == head and pr['merge_commit_sha'] == checkout
tree = git('rev-parse', head + '^{tree}')
assert tree == git('rev-parse', checkout + '^{tree}') == '653a46758f9e6af9662b77ecfe2f069a2ac94cb4'
assert pr['state'] == 'open' and pr['draft'] and not pr['merged']
assert pr['base_sha'] == '8f551de4423a6b64d9c0ac94fff9b9bd07264f2a'
assert len(jobs) == 15 and all(j['status'] == 'completed' and j['conclusion'] == 'success' for j in jobs)
assert len(live) == 15 and all(j['checkoutLines'] for j in live)
by_job, by_log = {j['id']: j for j in jobs}, {j['job_id']: j for j in live}
functional = load(ev / 'functional/functional-result.json')
log_manifest = load(ev / 'functional/raw-log-manifest.json')
job_checks, hashes = [], []
for row in functional['jobs']:
    job = by_job[row['job_id']]
    steps = {s['name']: s for s in job['steps']}
    assert all(steps[s]['conclusion'] == 'success' for s in row['required_steps_success'])
    saved = next(x for x in log_manifest if x['job_id'] == row['job_id'])
    f = ev / 'functional' / saved['file']
    packed, raw = f.read_bytes(), gzip.open(f, 'rb').read()
    assert digest(packed) == saved['gzip_sha256']
    assert digest(raw) == saved['raw_utf8_sha256'] == by_log[row['job_id']]['sha256']
    assert len(raw) == saved['raw_utf8_bytes'] == by_log[row['job_id']]['bytes']
    text = raw.decode('utf-8')
    assert checkout in text
    test_fails = re.findall(r'--- FAIL: [^\r\n]+', text)
    test_skips = re.findall(r'--- SKIP: [^\r\n]+', text)
    assert not test_fails and not test_skips
    job_checks.append({'id': job['id'], 'name': job['name'], 'conclusion': job['conclusion'],
                       'fresh_raw_log_sha256': digest(raw), 'fresh_matches_saved_log': True,
                       'required_steps': row['required_steps_success'],
                       'conditional_skipped_steps': [s['name'] for s in job['steps'] if s['conclusion'] == 'skipped'],
                       'named_test_failures': len(test_fails), 'named_test_skips': len(test_skips)})
    hashes.append({'path': str(f.relative_to(repo)), 'sha256': digest(packed), 'raw_sha256': digest(raw)})
mixed = by_job[133280386571]
assert all(next(s for s in mixed['steps'] if s['name'] == n)['conclusion'] == 'success' for n in
           ['Measure mixed HTTP load and pool recovery', 'Qualify sustained fixed-arrival HTTP load'])
job_checks.append({'id': mixed['id'], 'name': mixed['name'], 'conclusion': mixed['conclusion'],
                   'fresh_raw_log_sha256': by_log[mixed['id']]['sha256'],
                   'steps_counted_as_one_job': True, 'performance_csv_review': 'Separate Agent A'})

source_checks = {'head': head, 'actual_checkout': checkout, 'verified_tree': tree,
                 'synthetic_parents': git('show', '-s', '--format=%P', checkout).split(),
                 'post_test_docs_head': git('rev-parse', 'HEAD'),
                 'post_test_docs_tree': git('rev-parse', 'HEAD^{tree}'),
                 'post_test_changed_paths': git('diff', '--name-only', head, 'HEAD').splitlines(),
                 'focused_qa_source': focused,
                 'focused_to_test_changes': git('diff', '--name-only', focused, head).splitlines()}
assert all(p.startswith('docs/') for p in source_checks['post_test_changed_paths'])
assert all(p.startswith('docs/workstreams/') for p in source_checks['focused_to_test_changes'])
source_checks['workflow_blobs'] = {p: git('rev-parse', f'{head}:{p}') for p in
    ['.github/workflows/full-qualification.yml', '.github/workflows/backend.yml',
     '.github/workflows/security.yml', '.github/workflows/frontend.yml',
     '.github/workflows/nonperformance-e2e.yml', '.github/workflows/source-security.yml']}
source_checks['schema_latest'] = max(map(int, re.findall(r'Version: (\d+)', git('show', f'{head}:migrations/system/embed.go'))))
assert source_checks['schema_latest'] == 69

security_dir = ev / 'independent/security'
baseline = ws / 'evidence/phase-nonperformance-security-ci/ci-43836410887'
artifact_checks = []
for f in sorted(security_dir.glob('*.zip')):
    h = digest(f.read_bytes())
    matching = [a for a in arts if a['digest'] == 'sha256:' + h]
    assert len(matching) == 1
    a = matching[0]
    assert a['size_in_bytes'] == f.stat().st_size and not a['expired']
    assert a['workflow_run']['id'] == 43861850923 and a['workflow_run']['head_sha'] == head
    artifact_checks.append({'file': str(f.relative_to(repo)), 'id': a['id'], 'sha256': h,
                            'bytes': f.stat().st_size, 'matches_live_github_digest': True,
                            'expires_at': a['expires_at']})
    hashes.append({'path': str(f.relative_to(repo)), 'sha256': h})

def sarif(path, filename):
    raw = zipfile.ZipFile(path).read(filename)
    return json.loads(raw)['runs'][0], raw

def resolve_rule(run, result):
    ref = result.get('rule', {})
    component = ref.get('toolComponent', {})
    rules = run['tool']['extensions'][component['index']]['rules'] if 'index' in component else run['tool']['driver']['rules']
    if 'index' in ref:
        return rules[ref['index']]
    return next(r for r in rules if r['id'] == result['ruleId'])

def location(run, loc):
    physical = loc.get('physicalLocation', {})
    artifact = physical.get('artifactLocation', {})
    uri = artifact.get('uri')
    if uri is None and 'index' in artifact:
        uri = run['artifacts'][artifact['index']]['location']['uri']
    return {'uri': uri, 'region': physical.get('region')}

def reported_paths(run):
    found = set()
    def visit(obj):
        if isinstance(obj, dict):
            if 'physicalLocation' in obj:
                uri = location(run, obj)['uri']
                if uri:
                    found.add(uri)
            for value in obj.values():
                visit(value)
        elif isinstance(obj, list):
            for value in obj:
                visit(value)
    visit(run['results'])
    return found

security = {'source': {}, 'image': {}, 'scope': 'Existing artifact comparison and retained prior dispositions only; no new security scan or reachability audit.'}
reported = set()
all_severities = collections.Counter()
for label, filename in [('go', 'go.sarif'), ('js', 'javascript.sarif')]:
    old, _ = sarif(baseline / (label + '.zip'), filename)
    new, raw = sarif(security_dir / (label + '.zip'), filename)
    summary = json.loads(zipfile.ZipFile(security_dir / (label + '.zip')).read('summary.json'))
    assert summary['sourceCommit'] == checkout and str(summary['workflowRun']) == '43861850923'
    assert summary['reports'][0]['sha256'] == digest(raw) and not summary['reports'][0]['diffRangeRestricted']
    def identity(run, r):
        return json.dumps({'ruleId': r['ruleId'], 'message': r['message'],
                           'partialFingerprints': r.get('partialFingerprints'),
                           'primary_locations': [location(run, x) for x in r.get('locations', [])],
                           'rule_properties': resolve_rule(run, r).get('properties')}, sort_keys=True)
    before = collections.Counter(identity(old, r) for r in old['results'])
    after = collections.Counter(identity(new, r) for r in new['results'])
    assert before == after
    assert all(x['executionSuccessful'] for x in new['invocations'])
    warnings = [x for i in new['invocations'] for x in i.get('toolExecutionNotifications', []) if x.get('level') in ['warning', 'error']]
    assert not warnings
    rules = collections.Counter()
    score_groups = collections.Counter()
    for r in new['results']:
        rule = resolve_rule(new, r)
        score = float(rule['properties']['security-severity'])
        severity = 'CRITICAL' if score >= 9 else 'HIGH' if score >= 7 else 'MEDIUM' if score >= 4 else 'LOW'
        score_groups[severity] += 1
        rules[(rule['id'], str(score), r.get('level', rule.get('defaultConfiguration', {}).get('level')))] += 1
    all_severities.update(score_groups)
    reported |= reported_paths(old) | reported_paths(new)
    security['source'][label] = {'count': len(new['results']), 'raw_summary_classification': summary['classification'],
                                'identities_primary_locations_and_rule_metadata_match_baseline': True,
                                'new_identities': 0, 'rule_security_severity_counts': dict(score_groups),
                                'rule_counts': [{'id': k[0], 'score': k[1], 'level': k[2], 'count': v} for k, v in sorted(rules.items())],
                                'extracted_count': summary['reports'][0]['successfullyExtractedFileCount'],
                                'successful_invocations': True, 'extraction_warning_error_count': len(warnings)}
security['source_reported_flow_blobs_unchanged'] = all(git('rev-parse', f'56a881f47d0c27877b91959d7df641c2f603ef03:{p}') == git('rev-parse', f'{head}:{p}') for p in sorted(reported))
assert security['source_reported_flow_blobs_unchanged']
security['raw_source_score_counts'] = dict(all_severities)
security['source_classification_reference'] = 'final-security.md#independent-classification-of-all-24-go--one-js-locations and phase-nonperformance-security-ci.md'
old, _ = sarif(baseline / 'image.zip', 'pgbase-image-all-severities.sarif')
new, _ = sarif(security_dir / 'image.zip', 'pgbase-image-all-severities.sarif')
high, _ = sarif(security_dir / 'image.zip', 'pgbase-image.sarif')
assert old['results'] == new['results'] and old['tool']['driver']['rules'] == new['tool']['driver']['rules']
counts = collections.Counter(re.search(r'Severity: (\w+)', r['message']['text'])[1] for r in new['results'])
assert not high.get('results') and counts == {'MEDIUM': 21, 'LOW': 9, 'UNKNOWN': 2}
security['image'] = {'count': len(new['results']), 'counts': dict(counts), 'high_critical_gate_results': len(high.get('results', [])), 'exact_baseline_result_and_rule_metadata_match': True}
security['retained_image_dispositions'] = {'libc_native_helper_risks': 25, 'openssl_upstream_distribution_conflicts': 4, 'CMS_record_without_demonstrated_entry': 1, 'architecture_specific_amd64_limit': 2, 'UNKNOWN_native_records_remain_unresolved': 2}
security['new_blocking_findings'] = 0
security['not_a_zero_vulnerability_claim'] = True

qa = {}
for mode in ['red', 'green-race']:
    p = ev / 'independent' / f'listener-{mode}.jsonl'
    events = [json.loads(x) for x in p.read_text().splitlines() if x.strip()]
    passed = [e for e in events if e['Action'] == 'pass' and 'Test' in e]
    failed = [e for e in events if e['Action'] == 'fail' and 'Test' in e]
    skipped = [e for e in events if e['Action'] == 'skip' and 'Test' in e]
    qa[mode] = {'sha256': digest(p.read_bytes()), 'top_level_passes': sum('/' not in e['Test'] for e in passed),
                'subtest_passes': sum('/' in e['Test'] for e in passed), 'failed_tests': [e['Test'] for e in failed],
                'skipped_tests': [e['Test'] for e in skipped], 'exit': int(p.with_suffix('.exit').read_text()),
                'passed_tests': [e['Test'] for e in passed]}
    hashes.append({'path': str(p.relative_to(repo)), 'sha256': digest(p.read_bytes())})
assert qa['red']['failed_tests'] and qa['red']['exit'] != 0
assert qa['green-race']['top_level_passes'] == 25 and qa['green-race']['subtest_passes'] == 17
assert not qa['green-race']['failed_tests'] and not qa['green-race']['skipped_tests'] and qa['green-race']['exit'] == 0
listener = git('show', f'{head}:internal/schema/listener.go')
pool = git('show', f'{head}:internal/database/pool.go')
assert 'pooledConnection.Hijack()' in listener and 'connection.Close(cleanupCtx)' in listener
assert 'database.BoundedCleanupContext(ctx)' in listener and 'l.pid.Store(0)' in listener
assert 'config.AfterConnect =' in pool and 'pg_try_advisory_lock_shared' in pool and 'configureSession(ctx, conn.Exec, statementTimeout)' in pool
assert git('rev-parse', f'{head}:internal/schema/listener.go') == git('rev-parse', f'{focused}:internal/schema/listener.go')
qa['scope'] = 'Re-read prior RED/GREEN results and matching final code; no new test execution in closeout.'
qa['connection_budget'] = {'request_background_pool': 4, 'dedicated_listener': 1, 'existing_backup_gate': 1, 'diagnostic_observer_when_enabled': 1, 'diagnostic_total': 7, 'prior_diagnostic_total': 6, 'observer_always_present_in_production': False}
qa['preserved_contracts'] = ['AfterConnect secure session', 'backup shared writer advisory lock', 'reconnect capacity', 'bounded close and PID cleanup', 'backup exclusion and recovery', 'schema readiness', 'shutdown ordering']

result = {'verdict': 'PASS', 'review_type': 'Independent existing-evidence reconciliation only',
          'source': source_checks, 'full_ci': {'success': 15, 'failed': 0, 'unfinished': 0, 'jobs': job_checks,
          'all_15_fresh_logs_have_actual_checkout': True, 'all_14_functional_saved_logs_equal_live_bytes': True},
          'artifacts_verified_locally_against_live_metadata': artifact_checks, 'security': security, 'prior_qa': qa,
          'retained_limitations': ['Firefox historical font cancellation not causally repaired; final gate passed', 'External OAuth/OIDC/SMTP interoperability not exhaustively qualified', 'Storage ambiguous-write tombstones and writer quiescence', 'External Jobs effect idempotency/fencing', 'Single CI workload/environment capacity only', 'Historical 547 drops remain failed evidence'],
          'evidence_hashes': hashes}
(out / 'source-ci-verification.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps({'verdict': result['verdict'], 'tree': tree, 'jobs': len(jobs), 'matching_live_functional_logs': len(functional['jobs']), 'security': security['raw_source_score_counts'], 'image': security['image'], 'prior_race_top_level': qa['green-race']['top_level_passes'], 'prior_race_subtests': qa['green-race']['subtest_passes']}))
