import { describe, it, expect } from 'vitest';
import { validateRun, parseProperties, validatePlan } from '../src/domain.js';

describe('new run validation', () => {
  it('requires a name, immutable script version and at least one worker', () => {
    expect(validateRun({ name: ' ', plan_id: '', workers: [] })).toEqual({ name: '请输入任务名称', plan_id: '请选择脚本版本', cluster_id: '请选择执行集群', workers: '至少选择 1 个执行节点' });
  });
  it('rejects workers outside the selected cluster', () => {
    expect(validateRun({ name: '自测', plan_id: 'plan-1', cluster_id: 'c1', workers: ['worker-7'] }, ['worker-2']).workers).toBe('执行节点必须属于所选集群，且不可重复');
  });
  it('allows a valid subset and recognizes per-worker global properties', () => {
    expect(validateRun({ name: '自测', plan_id: 'plan-1', cluster_id: 'c1', workers: ['worker-2', 'worker-4'] }, ['worker-2', 'worker-4'])).toEqual({});
    expect(parseProperties('threads=10\nbase_url=http://target.local:8080?a=b')).toEqual({ threads: '10', base_url: 'http://target.local:8080?a=b' });
  });
  it('rejects reserved execution properties and duplicates', () => {
    expect(() => parseProperties('server.rmi.ssl.disable=true')).toThrow('保留属性');
    expect(() => parseProperties('threads=10\nthreads=20')).toThrow('重复');
    expect(() => parseProperties('bad key=1')).toThrow('属性名称');
  });
});
describe('script upload', () => {
  it('rejects oversize and dangerous XML declarations before upload', () => {
    expect(validatePlan('a.jmx', '<!DOCTYPE x><jmeterTestPlan/>', 10)).toMatch('DOCTYPE');
    expect(validatePlan('a.jmx', '<jmeterTestPlan/>', 5242881)).toMatch('5 MiB');
    expect(validatePlan('a.jmx', '<jmeterTestPlan/>', 20)).toBe('');
  });
});

describe('single-manager cluster readiness', () => {
  it('accepts one healthy manager without a reporter and rejects a failed worker', async () => {
    const { clusterReady } = await import('../src/domain.js');
    const cluster = { clusters: [{ registered: true, worker_ids: ['worker-a'], workers: [{ id: 'worker-a', healthy: true }], controller: { healthy: true } }], manager: { leader: 'manager', instances: [{ id: 'manager', healthy: true }] } };
    expect(clusterReady(cluster)).toBe(true);
    cluster.clusters[0].workers[0].healthy = false;
    expect(clusterReady(cluster)).toBe(false);
    expect(clusterReady(null)).toBe(false);
  });
});

it('keeps exact file byte sizes and never invents missing sizes or unsafe download URLs', async () => {
  const { formatBytes, artifactHref } = await import('../src/domain.js');
  expect(formatBytes(1234)).toBe('1,234 B');
  expect(formatBytes(0)).toBe('0 B');
  expect(formatBytes(null)).toBe('—');
  expect(artifactHref('run-1', { name: 'results.jtl', download_url: '/api/runs/run-1/artifacts/results.jtl' })).toBe('/api/runs/run-1/artifacts/results.jtl');
  expect(artifactHref('run-1', { name: '../secret', download_url: '/api/runs/run-1/artifacts/../secret' })).toBeNull();
  expect(artifactHref('run-1', { name: 'results.jtl', download_url: 'https://external.example/results.jtl' })).toBeNull();
});
it('locks engine settings without rejecting business keys that share exact-name prefixes',()=>{
 for(const key of ['server_port','remote_hosts','mode','user.properties','system.properties','search_paths','plugin_dependency_paths','user.classpath','engine_id','remote.exit','jmeterengine.force.system.exit','beanshell.server.port']) expect(()=>parseProperties(`${key}=value`)).toThrow('保留属性');
 expect(parseProperties('model=gpt\nmodel_id=one\nmode_hint=custom\nserver_port_hint=business\nengine_id_label=business')).toEqual({model:'gpt',model_id:'one',mode_hint:'custom',server_port_hint:'business',engine_id_label:'business'});
});
