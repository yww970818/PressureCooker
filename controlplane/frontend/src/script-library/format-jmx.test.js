// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { formatJmxPreview } from './format-jmx.js';

it('indents nested JMX containers and is stable on already formatted input', () => {
  const raw = '<?xml version="1.0" encoding="UTF-8"?><jmeterTestPlan><hashTree><!-- x > y --><TestPlan testname="a > b"/><hashTree/></hashTree></jmeterTestPlan>';
  const formatted = formatJmxPreview(raw);
  expect(formatted).toBe('<?xml version="1.0" encoding="UTF-8"?>\n<jmeterTestPlan>\n  <hashTree>\n    <!-- x > y -->\n    <TestPlan testname="a > b"/>\n    <hashTree/>\n  </hashTree>\n</jmeterTestPlan>');
  expect(formatJmxPreview(formatted)).toBe(formatted);
});

it('renders XML-encoded structural whitespace as indentation while retaining encoded property values', () => {
  const property = '<stringProp name="body">&#xA;  {&quot;value&quot;: 1}&#xA;</stringProp>';
  expect(formatJmxPreview('<jmeterTestPlan>&#xA;  <hashTree>&#10;' + property + '&#xD;&#x9;&#32;</hashTree>&#xA;</jmeterTestPlan>')).toBe('<jmeterTestPlan>\n  <hashTree>\n    ' + property + '\n  </hashTree>\n</jmeterTestPlan>');
});

it('preserves exact property payloads, CDATA, mixed content, entities and xml:space', () => {
  const properties = [
    '<stringProp name="json">  {&quot;query&quot;: &quot;a &gt; b&quot;}\n  </stringProp>',
    '<stringProp name="script"><![CDATA[if (a < b) {\n  log.info("<fake> </fake>")\n}]]></stringProp>',
    '<stringProp name="blank"> \n  </stringProp>',
    '<mixed> before <b>value</b> after </mixed>',
    '<preserved xml:space="preserve"> <nested><value/></nested> </preserved>',
    '<preserved xml:space="pre&#115;erve"> <nested><value/></nested> </preserved>',
    '<preserved note="xml:space=\'default\'" xml:space="preserve"> <nested><value/></nested> </preserved>',
  ];
  const raw = '<jmeterTestPlan><hashTree>' + properties.join('') + '</hashTree></jmeterTestPlan>';
  const formatted = formatJmxPreview(raw);
  for (const value of properties) expect(formatted).toContain(value);
  const parse = value => new DOMParser().parseFromString(value, 'application/xml');
  expect([...parse(formatted).querySelectorAll('stringProp, mixed, preserved')].map(node => node.textContent)).toEqual([...parse(raw).querySelectorAll('stringProp, mixed, preserved')].map(node => node.textContent));
});

it('retains declarations and internal entities without resolving or rewriting them', () => {
  const doctype = '<!DOCTYPE root [<!ENTITY message "a > b">]>';
  expect(formatJmxPreview(doctype + '<root><value>&message;</value></root>')).toBe(doctype + '\n<root>\n  <value>&message;</value>\n</root>');
});

it('preserves non-XML whitespace in mixed content and brackets inside DTD comments', () => {
  const mixed = '<root>\u00a0\u2003<b/></root>';
  expect(formatJmxPreview(mixed)).toBe(mixed);
  const doctype = '<!DOCTYPE root [<!-- ] > --><?sample ] >?><!ELEMENT root ANY>]>';
  const formatted = formatJmxPreview(doctype + '<root><value/></root>');
  expect(formatted).toBe(doctype + '\n<root>\n  <value/>\n</root>');
  expect(new DOMParser().parseFromString(formatted, 'application/xml').documentElement.tagName).toBe('root');
});

it.each(['', '<root><open></root>', '<root><value>&unknown;</value></root>', '<root a="unfinished>'])('falls back to the original for empty or invalid source %s', raw => {
  expect(formatJmxPreview(raw)).toBe(raw);
});

it('retains CRLF and formats thousands of sibling components without recursion', () => {
  const raw = '<?xml version="1.0"?>\r\n<jmeterTestPlan><hashTree>' + '<HTTPSamplerProxy><stringProp name="path">/api</stringProp></HTTPSamplerProxy><hashTree/>'.repeat(5000) + '</hashTree></jmeterTestPlan>\r\n';
  const formatted = formatJmxPreview(raw);
  expect(formatted.split('<HTTPSamplerProxy>')).toHaveLength(5001);
  expect(formatted).toContain('\r\n    <HTTPSamplerProxy>\r\n      <stringProp name="path">/api</stringProp>\r\n    </HTTPSamplerProxy>');
  expect(formatted.endsWith('\r\n')).toBe(true);
});

it('formats imported XML fragments without losing opaque plugin subtrees or text', () => {
  const raw = '<org.vendor.Container/><hashTree><script><![CDATA[a < b]]></script><hashTree/></hashTree>';
  expect(formatJmxPreview(raw, { fragment: true })).toBe('<org.vendor.Container/>\n<hashTree>\n  <script><![CDATA[a < b]]></script>\n  <hashTree/>\n</hashTree>');
  expect(formatJmxPreview(raw)).toBe(raw);
  for (const text of ['before<element/>', '<element/>after', '<element/><broken>', '<![CDATA[text]]><element/>', '<element/>\uFEFF<hashTree/>', '<element/>\uFEFF']) expect(formatJmxPreview(text, { fragment: true })).toBe(text);
});
