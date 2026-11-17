#!/usr/bin/env python3
"""Offline audit of an existing qualification artifact; never starts a server or benchmark."""
import argparse
import csv
import hashlib
import json
import math
import re
import sys
import subprocess
import zipfile
from collections import Counter
from decimal import Decimal
from pathlib import Path

FIELDS = ['index', 'kind', 'owner', 'scheduled_offset_ns', 'generator_queue_ns',
          'http_service_ns', 'scheduled_to_validated_ns', 'status', 'error',
          'generator_ns', 'worker_queue_ns']
COUNT, RATE, SECONDS = 120000, 200, 600

def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()

def duration_ns(value):
    scales = {'ns': 1, 'µs': 1000, 'us': 1000, 'ms': 1000000, 's': 1000000000, 'm': 60000000000}
    tokens = re.findall(r'([0-9.]+)(ns|µs|us|ms|s|m)', value)
    if not tokens or ''.join(a+b for a,b in tokens) != value:
        raise ValueError('Invalid Go duration: '+value)
    return int(sum(Decimal(a)*scales[b] for a,b in tokens))

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('raw_directory', type=Path)
    parser.add_argument('archive', type=Path)
    parser.add_argument('metadata', type=Path)
    parser.add_argument('job_log', type=Path)
    parser.add_argument('source_directory', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--candidate', required=True)
    parser.add_argument('--tree', required=True)
    parser.add_argument('--checkout', required=True)
    parser.add_argument('--run-id', required=True, type=int)
    parser.add_argument('--job-id', required=True, type=int)
    parser.add_argument('--artifact-id', required=True, type=int)
    args = parser.parse_args()
    CHECKOUT, CANDIDATE, TREE = args.checkout, args.candidate, args.tree
    root = args.raw_directory
    checks = {}
    def check(name, condition):
        checks[name] = bool(condition)

    metadata = json.loads(args.metadata.read_text())
    archive_sha = digest(args.archive)
    check('archive_sha256_matches_github_digest', metadata['digest'] == 'sha256:'+archive_sha)
    check('archive_bytes_match_github_metadata', args.archive.stat().st_size == metadata['size_in_bytes'])
    check('artifact_identifiers_match', metadata['id'] == args.artifact_id and metadata['workflow_run']['id'] == args.run_id and metadata['name'] == 'mixed-http-'+CHECKOUT)
    check('artifact_workflow_head_matches_candidate', metadata['workflow_run']['head_sha'] == CANDIDATE)
    check('artifact_not_expired_at_fetch', metadata['expired'] is False)
    expected_members = {'mixed-environment.txt', 'mixed-http.txt', 'open-loop-http.txt',
                        'load-evidence/host-observer-start.txt', 'load-evidence/host-vmstat.txt',
                        'load-evidence/database-container-stats.txt', 'load-evidence/open-loop-database.json',
                        'load-evidence/open-loop-samples.csv'}
    with zipfile.ZipFile(args.archive) as archive:
        members = [info.filename for info in archive.infolist() if not info.is_dir()]
        check('archive_has_exact_evidence_members_without_duplicates', set(members) == expected_members and len(members) == len(expected_members))
        check('zip_crc_valid', archive.testzip() is None)
        check('all_extracted_bytes_match_archive', all(name in members and (root/name).is_file() and archive.read(name) == (root/name).read_bytes() for name in expected_members))

    log = args.job_log.read_text()
    environment = (root/'mixed-environment.txt').read_text()
    check('artifact_checkout_sha_matches', environment.splitlines()[0] == CHECKOUT)
    check('job_log_contains_checkout_sha_and_uploaded_digest', CHECKOUT in log and archive_sha in log)
    summary = (root/'open-loop-http.txt').read_text()
    mixed = (root/'mixed-http.txt').read_text()
    check('open_loop_test_passed', '--- PASS: TestOpenLoopHTTPQualification' in summary and '--- FAIL:' not in summary)
    check('mixed_http_all_four_phases_passed', all('--- PASS: TestMixedHTTPPoolQualification/'+phase in mixed for phase in ['baseline', 'concurrent', 'saturated', 'recovery']) and '--- FAIL:' not in mixed)
    check('artifact_test_output_present_in_job_log', all(line in log for line in summary.splitlines() if line.strip()))

    indices, kinds, owners, statuses = Counter(), Counter(), Counter(), Counter()
    malformed = invalid_numeric = semantic_errors = schedule_errors = timing_errors = 0
    completed = attempted = errors = dropped = unscheduled = 0
    series = {name: [] for name in ['scheduled_to_validated', 'http_service', 'generator_and_queue', 'generator', 'worker_queue']}
    numeric = [field for field in FIELDS if field not in ['kind', 'error']]
    row_count = 0
    with (root/'load-evidence/open-loop-samples.csv').open(newline='') as stream:
        reader = csv.DictReader(stream, strict=True)
        check('csv_exact_header', reader.fieldnames == FIELDS)
        for row in reader:
            row_count += 1
            if None in row or any(v is None for v in row.values()):
                malformed += 1
                continue
            try:
                n = {key:int(row[key]) for key in numeric}
                if any(str(value) != row[key] for key,value in n.items()):
                    invalid_numeric += 1
            except (ValueError, TypeError):
                invalid_numeric += 1
                continue
            i = n['index']; indices[i] += 1
            kinds[row['kind']] += 1; owners[n['owner']] += 1; statuses[n['status']] += 1
            expected_kind = ['create','update','delete','get','page','page','page','page','page','page'][i % 10]
            expected_status = {'create':201, 'delete':204}.get(expected_kind, 200)
            if not (0 <= i < COUNT and i == row_count-1 and row['kind'] == expected_kind and n['owner'] == (i//10)%2 and n['status'] == expected_status):
                semantic_errors += 1
            if n['scheduled_offset_ns'] != i*(1000000000//RATE):
                schedule_errors += 1
            if not (n['generator_ns'] >= 0 and n['worker_queue_ns'] >= 0 and n['generator_queue_ns'] == n['generator_ns'] + n['worker_queue_ns'] and n['http_service_ns'] > 0 and n['scheduled_to_validated_ns'] >= n['generator_queue_ns'] + n['http_service_ns']):
                timing_errors += 1
            completed += n['status'] != 0
            attempted += n['http_service_ns'] > 0
            errors += bool(row['error'])
            dropped += row['error'] == 'arrival queue full'
            unscheduled += row['error'].startswith('arrival not scheduled:')
            if n['http_service_ns'] > 0:
                for name,column in [('scheduled_to_validated','scheduled_to_validated_ns'),('http_service','http_service_ns'),('generator_and_queue','generator_queue_ns'),('generator','generator_ns'),('worker_queue','worker_queue_ns')]:
                    series[name].append(n[column])
    missing_count = sum(i not in indices for i in range(COUNT))
    duplicate_count = sum(max(0,n-1) for n in indices.values())
    check('csv_120000_rows', row_count == COUNT)
    check('csv_no_malformed_or_noncanonical_numeric_fields', malformed == invalid_numeric == 0)
    check('csv_ids_exactly_0_through_119999_once', missing_count == duplicate_count == 0 and len(indices) == COUNT and set(indices) == set(range(COUNT)))
    check('csv_semantics_owner_workload_status_correct', semantic_errors == 0)
    check('csv_schedule_exactly_200_rps_600_seconds', schedule_errors == 0)
    check('csv_timing_fields_consistent', timing_errors == 0)
    check('csv_all_requests_attempted_completed_without_errors', attempted == completed == COUNT and errors == dropped == unscheduled == 0)

    quantiles = {}
    logged = {}
    for metric,samples,p50,p95,p99 in re.findall(r'metric=(\w+) samples=(\d+) p50=(\S+) p95=(\S+) p99=(\S+)', summary):
        logged[metric] = {'samples':int(samples), 'p50_ns':duration_ns(p50), 'p95_ns':duration_ns(p95), 'p99_ns':duration_ns(p99)}
    for name,values in series.items():
        values.sort()
        quantiles[name] = {'samples':len(values), **{'p%d_ns'%p:values[(len(values)*p+99)//100-1] for p in [50,95,99]}}
        check('raw_quantiles_match_ci_'+name, quantiles[name] == logged.get(name))
    check('preregistered_e2e_p99_gate_250ms', quantiles['scheduled_to_validated']['p99_ns'] <= 250000000)
    summary_line = next(line for line in summary.splitlines() if 'offered_rps=' in line)
    scalars = dict(re.findall(r'(\w+)=([^\s]+)', summary_line))
    expected = {'offered_rps':200,'seconds':600,'intended':COUNT,'enqueued':COUNT,'attempted':COUNT,'completed':COUNT,'dropped':0,'unscheduled':0,'errors':0,'pool_max':4}
    check('ci_summary_matches_preregistered_gate_and_raw_rows', all(int(scalars[k]) == v for k,v in expected.items()))

    database = json.loads((root/'load-evidence/open-loop-database.json').read_text())
    observations = database['observations']
    check('observer_has_samples_and_no_diagnostic_error', bool(observations) and not database.get('diagnostic_error'))
    scheduled = [o['scheduled_unix_ns'] for o in observations]
    trace_gaps = sum(b-a != 100000000 for a,b in zip(scheduled, scheduled[1:]))
    check('observer_monotonic_scheduled_timestamps', all(b > a for a,b in zip(scheduled, scheduled[1:])))
    check('observer_independent_100ms_cadence', database['interval_ms'] == 100 and 'Independent observer connection' in database['scope'])
    check('pool_max_4_all_observations', all(o['pool']['MaxConns'] == 4 and o['pool']['TotalConns'] <= 4 and o['pool']['AcquiredConns'] <= 4 for o in observations))
    settings = {phase:database[phase]['settings'] for phase in ['before','after']}
    check('durability_on_before_and_after', all(settings[phase][name] == 'on' for phase in settings for name in ['fsync','synchronous_commit','full_page_writes']))
    check('settings_unchanged_before_after', settings['before'] == settings['after'])
    check('postgres_17_9_captured', all(settings[phase]['server_version'].startswith('17.9 ') for phase in settings))
    check('seven_persistent_sessions_before_after', all(database[phase]['database']['numbackends'] == 7 for phase in ['before','after']))
    check('six_non_observer_persistent_sessions_in_all_samples', all(Counter(b['application'] for b in o['backends'])['pgbase'] == 5 and Counter(b['application'] for b in o['backends'])['pgbase-backup-gate'] == 1 for o in observations))
    elapsed_ns = duration_ns(scalars['elapsed'])
    check('raw_observer_elapsed_matches_summary', database['elapsed_ns'] == elapsed_ns)
    achieved = Decimal(completed)*Decimal(1000000000)/Decimal(elapsed_ns)
    check('achieved_rps_matches_ci_rounding', f'{achieved:.2f}' == scalars['achieved_rps'] == '200.00')
    check('load_start_matches_summary', f'load_start_unix_ns={database["load_start_unix_ns"]}' in summary)

    def source_git(*arguments):
        return subprocess.check_output(['git', '-C', str(args.source_directory), *arguments], text=True).strip()
    check('source_worktree_at_exact_candidate', source_git('rev-parse', 'HEAD') == CANDIDATE)
    check('source_tree_at_expected_candidate', source_git('rev-parse', 'HEAD^{tree}') == TREE)
    check('actual_ci_checkout_tree_equals_candidate_tree', source_git('rev-parse', CHECKOUT+'^{tree}') == TREE)
    check('source_worktree_is_clean', not source_git('status', '--porcelain'))
    source_path = args.source_directory/'pgbase/open_loop_load_integration_test.go'
    source = source_path.read_text()
    workflow = (args.source_directory/'.github/workflows/full-qualification.yml').read_text()
    # These anchors supplement manual read-through of exact-SHA source; they are not a replacement for it.
    anchors = ['query.Set("pool_max_conns", "4")', 'SELECT count(*),count(*) FILTER',
               'rows != 256+count/5 || creates != count/10 || updates != count/10',
               'owner/relation/unique identity mismatch after load',
               'len(seen) != 128+count/10 || ownerCreates != count/20 || ownerUpdates != count/20',
               'http.StatusNotFound', 'headers.Get("ETag") == request.etag',
               'page.Total != min(wantTotal, 10000)', 'page order/filter/owner/relation mismatch',
               'p99 > 250*time.Millisecond']
    check('executed_source_contains_integrity_and_contract_assertions', all(anchor in source for anchor in anchors))
    check('workflow_fixes_200rps_600s_diagnostics_and_pipefail', all(anchor in workflow for anchor in ["PGBASE_OPEN_LOOP_RPS: '200'", "PGBASE_OPEN_LOOP_SECONDS: '600'", "PGBASE_OPEN_LOOP_DIAGNOSTICS: '1'", 'set -euo pipefail', 'retention-days: 14']))

    results = {
        'verdict':'PASS' if all(checks.values()) else 'FAIL',
        'scope':'Independent offline verification of fresh final CI artifact and exact-SHA test assertions; this reviewer ran no benchmark, server, scanner or runtime test.',
        'provenance':{'repository':'yakisoba0728/PgBase','run_id':args.run_id,'job_id':args.job_id,'artifact_id':args.artifact_id,'ci_checkout_sha':CHECKOUT,'candidate_sha':CANDIDATE,'git_tree':TREE,'archive_sha256':archive_sha,'archive_bytes':args.archive.stat().st_size,'artifact_expires_at':metadata['expires_at']},
        'checks':checks,
        'raw_csv':{'rows':row_count,'unique_ids':len(indices),'missing_ids':missing_count,'duplicate_ids':duplicate_count,'malformed_rows':malformed,'noncanonical_or_invalid_numeric_rows':invalid_numeric,'semantic_errors':semantic_errors,'schedule_errors':schedule_errors,'timing_errors':timing_errors,'attempted':attempted,'completed':completed,'dropped':dropped,'unscheduled':unscheduled,'unexpected_errors':errors,'kinds':dict(kinds),'owners':dict(owners),'statuses':dict(statuses),'arrival_interval_ns':1000000000//RATE},
        'quantile_method':'Nearest rank: sorted_values[(n * percentile + 99) // 100 - 1], matching the unchanged Go test.',
        'quantiles':quantiles,
        'resource_summary_scalars':scalars,
        'performance':{'offered_rps':RATE,'scheduled_seconds':SECONDS,'elapsed_ns':elapsed_ns,'achieved_rps_unrounded':str(achieved),'achieved_rps_rounded':f'{achieved:.2f}','e2e_p99_limit_ns':250000000},
        'database_integrity':{'verdict':'PASS','basis':'Exact-source unconditional post-drain physical SQL counts plus full per-owner cursor traversal and cross-owner 404 assertions executed before the Go PASS marker; no detached raw row dump is present.','expected_physical_rows':24256,'expected_created':12000,'expected_updated':12000,'expected_deleted_targets':12000,'per_owner_rows':12128,'per_owner_created':6000,'per_owner_updated':6000,'contracts':['owner CRUD and mutation ETag','page sort/filter/relation expansion','complete cursor traversal and unique IDs','capped totalItems=10000 with totalCapped=true','cross-owner point read returns 404'],'schema_69':'Outside this performance artifact; cross-check with source/CI reviewer evidence.'},
        'database_environment':{'settings':settings,'observer_samples':len(observations),'scheduled_tick_gaps':trace_gaps,'first_scheduled_offset_ns':scheduled[0]-database['load_start_unix_ns'],'last_scheduled_offset_ns':scheduled[-1]-database['load_start_unix_ns'],'observer_error':database.get('diagnostic_error',''),'pool_max_counts':dict(Counter(o['pool']['MaxConns'] for o in observations)),'pool_total_counts':dict(Counter(o['pool']['TotalConns'] for o in observations)),'sampled_non_observer_backend_counts':dict(Counter(len(o['backends']) for o in observations)),'before_numbackends':database['before']['database']['numbackends'],'after_numbackends':database['after']['database']['numbackends'],'persistent_session_budget':'4 request pool + 1 dedicated listener + 1 backup gate + 1 diagnostic observer = 7. Observer is optional; sampled transient maintenance backends are additional and do not enlarge request pool.'},
        'limitations':['One GitHub hosted 4-vCPU fixture with application, HTTP client and generator sharing a process; diagnostics enabled.','DB integrity verified through executed assertions and Go PASS; physical database was disposable and is not rerun or dumped now.','The historical 547-drop failure remains a failure; this audit makes no causal attribution or cross-hardware performance guarantee.','Schema, complete CI, listener QA and scanner residue require separate final-source reconciliation.','Seven persistent clients describe this workload baseline. Active storage operations can add up to 64 coordination sessions per controller, plus maintenance/operator headroom.'],
        'file_sha256':{str(p.relative_to(root)):digest(p) for p in sorted(root.rglob('*')) if p.is_file()},
        'source_sha256':{name:digest(args.source_directory/name) for name in ['pgbase/open_loop_load_integration_test.go','pgbase/open_loop_diagnostics_integration_test.go','pgbase/mixed_load_integration_test.go','.github/workflows/full-qualification.yml']},
        'job_log_sha256':digest(args.job_log),
    }
    args.output.write_text(json.dumps(results, indent=2)+'\n')
    print(json.dumps({'verdict':results['verdict'],'checks_passed':sum(checks.values()),'checks_total':len(checks),'failed_checks':[name for name,passed in checks.items() if not passed],'rows':row_count,'e2e_p99_ns':quantiles['scheduled_to_validated']['p99_ns'],'http_p99_ns':quantiles['http_service']['p99_ns'],'archive_sha256':archive_sha,'output':str(args.output)},indent=2))
    return 0 if results['verdict'] == 'PASS' else 1

if __name__ == '__main__':
    sys.exit(main())
