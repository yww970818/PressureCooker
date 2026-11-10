import { expect, it, vi } from 'vitest';
import { parseVariableRule, resolveEnvironmentVariables, resolveVariableValues } from './variable-rules.js';
import { validateEnvironments, validateProjectOverrides, mergedProjectEnvironment } from './environments.js';

it('accepts only complete named random rules with canonical lengths from 1 to 256', () => {
  expect(parseVariableRule('ordinary {{token}}')).toBeNull();
  expect(parseVariableRule('{{$random.uuid}}')).toEqual({ kind: 'uuid' });
  expect(parseVariableRule('{{$random.hex(1)}}')).toEqual({ kind: 'hex', length: 1 });
  expect(parseVariableRule('{{$random.alnum(256)}}')).toEqual({ kind: 'alnum', length: 256 });
  for (const value of ['{{$random.hex(0)}}', '{{$random.hex(257)}}', '{{$random.hex(01)}}', '{{$random.hex(-1)}}', '{{$random.hex(1.5)}}', '{{$random.hex( 2)}}', '{{$random.regex(x)}}', 'prefix {{$random.uuid}}', '{{$random.uuid}} ', '{{$random.uuid', '$random.uuid', '{{$random.UUID}}', '{{$random.hex(32)}}\n', '{{$random.digits(12)}}\r\n']) expect(() => parseVariableRule(value)).toThrow(/规则/);
});

it('generates UUID v4 and allowed alphabets using getRandomValues without randomUUID', () => {
  const nativeCrypto = globalThis.crypto;
  vi.stubGlobal('crypto', { getRandomValues: bytes => nativeCrypto.getRandomValues(bytes) });
  try {
    const definitions = { id: '{{$random.uuid}}', hex: '{{$random.hex(32)}}', alnum: '{{$random.alnum(256)}}', digits: '{{$random.digits(12)}}', fixed: 'literal {{token}}' };
    const first = resolveVariableValues(definitions), second = resolveVariableValues(definitions);
    expect(first.id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(first.hex).toMatch(/^[0-9a-f]{32}$/); expect(first.alnum).toMatch(/^[A-Za-z0-9]{256}$/); expect(first.digits).toMatch(/^\d{12}$/);
    expect(first.fixed).toBe(definitions.fixed); expect(first.id).not.toBe(second.id); expect(definitions.id).toBe('{{$random.uuid}}');
  } finally { vi.unstubAllGlobals(); }
});

it('uses rejection sampling instead of modulo bias and never falls back to insecure randomness', () => {
  let calls = 0;
  vi.stubGlobal('crypto', { getRandomValues(bytes) { bytes.fill(calls++ === 0 ? 255 : 7); return bytes; } });
  try { expect(resolveVariableValues({ digits: '{{$random.digits(3)}}' }).digits).toBe('777'); expect(calls).toBeGreaterThan(1); }
  finally { vi.unstubAllGlobals(); }
  vi.stubGlobal('crypto', undefined);
  try { expect(() => resolveVariableValues({ id: '{{$random.uuid}}' })).toThrow(/随机/); expect(resolveVariableValues({ value: 'fixed' })).toEqual({ value: 'fixed' }); }
  finally { vi.unstubAllGlobals(); }
});

it('resolves only winning effective rows, preserves metadata sources, and rejects malformed declarations on save', () => {
  const project = { id: 'qa', base_host: 'https://qa.test', variables: [{ name: 'device', value: '{{$random.broken}}' }, { name: 'metadata', value: { default: '{{$random.hex(8)}}', secret: true } }] };
  const overrides = { qa: { variables: [{ name: 'device', value: 'fixed-collection' }] } };
  const effective = mergedProjectEnvironment(project, overrides), values = resolveEnvironmentVariables(effective.variables);
  expect(values).toMatchObject({ device: 'fixed-collection', base_host: 'https://qa.test' }); expect(values.metadata).toMatch(/^[0-9a-f]{8}$/);
  expect(project.variables[1].value.default).toBe('{{$random.hex(8)}}');
  expect(() => validateEnvironments({ qa: { variables: [{ name: 'base_host', value: 'https://qa.test' }, { name: 'device', value: '{{$random.hex(0)}}' }] } })).toThrow(/规则/);
  expect(() => validateProjectOverrides({ qa: { variables: [{ name: 'device', value: 'prefix {{$random.uuid}}' }] } })).toThrow(/规则/);
});
it('recognizes exact generic auto sources and resolves only a validated operation context', () => {
 expect(parseVariableRule('{{$auto.client_ip}}')).toEqual({kind:'client_ip'});expect(parseVariableRule('{{$auto.timestamp_ms}}')).toEqual({kind:'timestamp_ms'});
 const rules={arbitrary_address:'{{$auto.client_ip}}',arbitrary_time:'{{$auto.timestamp_ms}}',fixed:'x'};
 expect(resolveVariableValues(rules,{client_ip:'2001:db8::1',timestamp_ms:'1794180000123'})).toEqual({arbitrary_address:'2001:db8::1',arbitrary_time:'1794180000123',fixed:'x'});
 expect(resolveEnvironmentVariables([{name:'v',value:'{{$auto.client_ip}}'},{name:'v',value:'override'}])).toEqual({v:'override'});
 expect(rules.arbitrary_time).toBe('{{$auto.timestamp_ms}}');
 for(const bad of ['{{$auto.other}}','{{$auto.client_ip(12)}}','prefix {{$auto.client_ip}}','{{$auto.timestamp_ms}} ','$auto.timestamp_ms','{{$auto.CLIENT_IP}}'])expect(()=>parseVariableRule(bad)).toThrow(/规则/);
 for(const client_ip of ['', 'host.example','999.1.2.3','127.0.0.1:8080','[::1]'])expect(()=>resolveVariableValues({v:'{{$auto.client_ip}}'},{client_ip})).toThrow(/来源 IP/);
 for(const timestamp_ms of [undefined,'','0','1.2','1e12',123,'-1'])expect(()=>resolveVariableValues({v:'{{$auto.timestamp_ms}}'},{timestamp_ms})).toThrow(/时间戳/);
 expect(()=>resolveVariableValues({v:'{{$auto.client_ip}}'})).toThrow(/来源 IP/);
 expect(resolveVariableValues({v:'{{$auto.client_ip}}'},{client_ip:'192.0.2.3'}).v).toBe('192.0.2.3');
 expect(resolveVariableValues({v:'{{$auto.timestamp_ms}}'},{client_ip:'',timestamp_ms:'1794180000123'}).v).toBe('1794180000123');
 expect(()=>validateEnvironments({qa:{variables:[{name:'base_host',value:'https://qa.test'},{name:'v',value:'prefix {{$auto.client_ip}}'}]}})).toThrow(/规则/);
 expect(()=>validateProjectOverrides({qa:{variables:[{name:'v',value:'{{$auto.bad}}'}]}})).not.toThrow();
 expect(()=>resolveVariableValues({v:'{{$auto.bad}}'})).toThrow(/规则/);
});
