#!/usr/bin/env python3
"""Source-bound final PgBase load evidence report; no test execution or gate changes."""
import argparse, collections, csv, hashlib, json, math, re, statistics
from datetime import datetime
from pathlib import Path

def quantiles(values):
    values = sorted(values)
    return {'samples': len(values), **{f'p{p}_ms': values[(len(values)*p+99)//100-1]/1e6 if values else None for p in (50,95,99)}, 'max_ms': max(values)/1e6 if values else None}

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory',type=Path)
    parser.add_argument('output',type=Path)
    parser.add_argument('--run-id',required=True)
    parser.add_argument('--job-id',required=True)
    parser.add_argument('--source-sha',required=True)
    parser.add_argument('--checkout-sha',required=True)
    parser.add_argument('--tree',required=True)
    args=parser.parse_args()
    samples=next(args.directory.rglob('open-loop-samples.csv'))
    dbfile=next(args.directory.rglob('open-loop-database.json'))
    with samples.open(newline='') as f: rows=list(csv.DictReader(f))
    if len(rows)!=120000: raise ValueError(f'Expected 120000 rows, found {len(rows)}')
    expected_kind={0:'create',1:'update',2:'delete',3:'get'}
    expected_status={'create':201,'update':200,'delete':204,'get':200,'page':200}
    for i,r in enumerate(rows):
        assert int(r['index'])==i
        assert int(r['scheduled_offset_ns'])==i*5000000
        assert r['kind']==expected_kind.get(i%10,'page')
        assert int(r['owner'])==(i//10)%2
    attempted=[r for r in rows if int(r['http_service_ns'])>0]
    completed=[r for r in rows if int(r['status'])!=0]
    failed=[r for r in rows if r['error']]
    dropped=[r for r in rows if r['error']=='arrival queue full']
    unscheduled=[r for r in rows if r['error'].startswith('arrival not scheduled:')]
    unexpected=[r for r in failed if r['error']!='arrival queue full' and not r['error'].startswith('arrival not scheduled:')]
    unexpected_status=[int(r['index']) for r in completed if int(r['status'])!=expected_status[r['kind']]]
    columns=['scheduled_to_validated_ns','http_service_ns','generator_queue_ns','generator_ns','worker_queue_ns']
    for r in attempted:
        assert all(int(r[c])>=0 for c in columns)
        assert int(r['generator_queue_ns'])==int(r['generator_ns'])+int(r['worker_queue_ns'])
        assert int(r['scheduled_to_validated_ns'])>=int(r['generator_queue_ns'])+int(r['http_service_ns'])
    latencies={c:quantiles([int(r[c]) for r in attempted]) for c in columns}
    counts={'scheduled':len(rows),'attempted':len(attempted),'completed':len(completed),'dropped':len(dropped),'unscheduled':len(unscheduled),'errors_including_drops':len(failed),'unexpected_errors_excluding_drop_unscheduled':len(unexpected),'incomplete_scheduled':len(rows)-len(completed),'incomplete_enqueued':len(rows)-len(dropped)-len(unscheduled)-len(completed)}
    by_kind={k:{'scheduled':sum(r['kind']==k for r in rows),'completed':sum(r['kind']==k for r in completed),'errors':sum(r['kind']==k for r in failed)} for k in expected_status}
    bins=[]
    for t in range(0,600,10):
        group=rows[t*200:(t+10)*200]
        bins.append({'scheduled_seconds':[t,t+9],'scheduled':len(group),'dropped':sum(r['error']=='arrival queue full' for r in group),'errors':sum(bool(r['error']) for r in group),'e2e':quantiles([int(r['scheduled_to_validated_ns']) for r in group if int(r['http_service_ns'])>0])})
    db=json.loads(dbfile.read_text())
    observations=db['observations']
    pools=collections.Counter(o['pool']['AcquiredConns'] for o in observations)
    backend_counts=collections.Counter(len(o['backends'] or []) for o in observations)
    applications=collections.Counter()
    maintenance_queries=collections.Counter()
    persistent_pids=collections.Counter()
    waitcounts=collections.Counter()
    querycounts=collections.Counter()
    for o in observations:
        for b in o['backends'] or []:
            applications[b['application']]+=1
            if b['query_prefix'].startswith('autovacuum:'): maintenance_queries[b['query_prefix']]+=1
            if b['application'] in ('pgbase','pgbase-backup-gate'): persistent_pids[str(b['pid'])]+=1
            if b['state']=='active':
                waitcounts[b['wait_type']+'/'+b['wait']]+=1
                querycounts[b['query_prefix']]+=1
    deltas={}
    for section in ('database','cluster_wal'):
        b,a=db['before'][section],db['after'][section]
        deltas[section]={k:v-b[k] for k,v in a.items() if isinstance(v,(int,float)) and isinstance(b.get(k),(int,float)) and v!=b[k]}
    for bucket in bins:
        lo=bucket['scheduled_seconds'][0]
        os=[o for o in observations if lo<=((o['started_unix_ns']-db['load_start_unix_ns'])/1e9)<lo+10]
        bucket['db_samples']=len(os)
        bucket['full_pool_samples']=sum(o['pool']['AcquiredConns']==4 for o in os)
        bucket['active_wal_wait_observations']=sum(b['state']=='active' and b['wait'] in ('WALWrite','WalSync') for o in os for b in o['backends'] or [])
    owner_reconciliation={}
    for owner in (0,1):
        successful=[r for r in rows if int(r['owner'])==owner and not r['error'] and int(r['status'])==expected_status[r['kind']]]
        creates=sum(r['kind']=='create' for r in successful)
        updates=sum(r['kind']=='update' for r in successful)
        deletes=sum(r['kind']=='delete' for r in successful)
        owner_reconciliation[str(owner)]={'seeded_rows':12128,'successful_creates':creates,'successful_updates':updates,'successful_deletes':deletes,'rows_reconstructed_from_successful_responses':12128+creates-deletes,'expected_rows_on_full_success':12128,'expected_creates_on_full_success':6000,'expected_updates_on_full_success':6000,'qualification':'HTTP-side arithmetic; compare actual final DB assertions and traversed owner identities in original test log'}
    settings_before=db['before']['settings']; settings_after=db['after']['settings']
    missing_settings=[phase+':'+k for phase,s in (('before',settings_before),('after',settings_after)) for k in ('fsync','synchronous_commit','full_page_writes') if k not in s]
    settings_valid=None if missing_settings else all(s.get(k)=='on' for s in (settings_before,settings_after) for k in ('fsync','synchronous_commit','full_page_writes'))
    environment_files=list(args.directory.rglob('mixed-environment.txt'))
    checked_checkout=environment_files[0].read_text().splitlines()[0].strip() if environment_files else None
    assert checked_checkout==args.checkout_sha, f'Artifact checkout {checked_checkout} != {args.checkout_sha}'
    pool_valid=bool(observations) and all(o['pool']['MaxConns']==4 for o in observations)
    p99=latencies['scheduled_to_validated_ns']['p99_ms']
    csv_gate=counts['completed']==120000 and not failed and counts['incomplete_enqueued']==0 and p99 is not None and p99<=250 and not unexpected_status
    logfiles=list(args.directory.rglob('open-loop-http.txt'))
    log=logfiles[0].read_text() if logfiles else ''
    logged_lines=[line for line in log.splitlines() if 'offered_rps=' in line]
    logged_summary=dict(re.findall(r'([a-z_]+)=([^ ]+)',logged_lines[-1])) if logged_lines else {}
    for k,v in (('intended',counts['scheduled']),('attempted',counts['attempted']),('completed',counts['completed']),('dropped',counts['dropped']),('unscheduled',counts['unscheduled']),('errors',counts['errors_including_drops'])):
        assert int(logged_summary[k])==v, f'Log/CSV disagreement: {k}'
    host_files=list(args.directory.rglob('host-vmstat.txt'))
    aligned_host=[]
    if host_files:
        for line in host_files[0].read_text().splitlines()[3:]:
            fields=line.split()
            if len(fields)!=20: continue
            timestamp=datetime.fromisoformat(fields[-2]+'T'+fields[-1]+'+00:00').timestamp()
            if db['load_start_unix_ns']/1e9<=timestamp<(db['load_start_unix_ns']+db['elapsed_ns'])/1e9:
                aligned_host.append(dict(zip(['r','b','swpd','free','buff','cache','si','so','bi','bo','in','cs','us','sy','id','wa','st','gu'],map(int,fields[:-2]))))
    host_summary={'aligned_samples':len(aligned_host),'scope':'Whole-host one-second vmstat samples in UTC; first since-boot sample excluded; not request-specific or isolated server CPU/RSS','columns':{k:{'mean':statistics.mean(r[k] for r in aligned_host),'max':max(r[k] for r in aligned_host),'min':min(r[k] for r in aligned_host)} for k in ('r','b','swpd','si','so','us','sy','id','wa','st','free') if aligned_host}}
    log_pass=bool(re.search(r'--- PASS: TestOpenLoopHTTPQualification',log)) and not re.search(r'--- FAIL:|\bFAIL\s+github.com/',log)
    result={'run_id':args.run_id,'job_id':args.job_id,'source_sha':args.source_sha,'checkout_sha':args.checkout_sha,'tree':args.tree,'artifact_checkout_verified':checked_checkout,'source_of_truth':'original CSV, diagnostics JSON and Go assertion log; no load rerun','counts':counts,'logged_summary':logged_summary,'host_vmstat':host_summary,'error_messages':dict(collections.Counter(r['error'] for r in failed)),'unexpected_status_indices':unexpected_status,'latencies':latencies,'by_kind':by_kind,'ten_second_buckets':bins,'owner_reconciliation':owner_reconciliation,'database':{'diagnostic_error':db.get('diagnostic_error'),'samples':len(observations),'settings_before':settings_before,'settings_after':settings_after,'missing_durability_settings':missing_settings,'pool_acquired_histogram':dict(pools),'sampled_backends_excluding_observer_histogram':dict(backend_counts),'application_observations':dict(applications),'maintenance_query_observations':dict(maintenance_queries),'persistent_application_pid_observations':dict(persistent_pids),'active_wait_observations':dict(waitcounts),'active_query_observations':dict(querycounts.most_common(20)),'counter_deltas':deltas,'observer_query':quantiles([o['query_ns'] for o in observations])},'gate_checks':{'complete_csv_gate':csv_gate,'max_request_pool_four':pool_valid,'durability_all_on_before_after':settings_valid,'go_assertions_passed':log_pass,'diagnostics_clean':not db.get('diagnostic_error')},'gate_result':'Pass' if all((csv_gate,pool_valid,settings_valid,log_pass,not db.get('diagnostic_error'))) else 'Fail','limitations':['One disposable CI host is not a production capacity guarantee.','Listener separation changes total connection budget by one; do not claim an isolated speedup versus historical hosts.','Percentiles are ranked independently and cannot be summed.','DB wait samples/query ages and cluster-wide WAL counters are correlations, not per-request causal durations.','Generator timestamp of a rejected arrival and continuous queue occupancy were not retained by this unchanged harness.','Dropped rows are errors; no fabricated latency is imputed.','Owner response arithmetic is secondary to original post-load durable DB and identity assertions.'],'files_sha256':{str(p.relative_to(args.directory)):hashlib.sha256(p.read_bytes()).hexdigest() for p in args.directory.rglob('*') if p.is_file()}}
    args.output.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps({k:result[k] for k in ('gate_result','counts','latencies','gate_checks')},indent=2))
if __name__=='__main__': main()
