import { defineComponent, h, markRaw } from 'vue';
import { parseMimeType } from '@scalar/helpers/http/mime-type';
import LocalizedResponseBody from './LocalizedResponseBody.vue';

const rawComponent = markRaw(LocalizedResponseBody);
const previewComponent = markRaw(defineComponent({
  props: ['content', 'contentType'],
  setup: props => () => h(LocalizedResponseBody, { content: props.content, contentType: props.contentType, pretty: true }),
}));
const decode = (buffer, contentType) => new TextDecoder(parseMimeType(contentType).parameters.get('charset')).decode(buffer);
/** Supported Scalar responseBody extension; all request/response orchestration stays in Scalar. */
export const responsePlugins = [{ responseBody: [
  { mimeTypes: ['application/json', '*+json', 'text/json', 'application/dns-json', 'application/ndjson', 'application/x-ndjson'], decode, rawComponent, previewComponent },
  { mimeTypes: ['text/*', 'application/xml', 'application/*+xml', 'application/yaml', 'application/javascript', 'application/rtf', 'application/x-sh', 'application/x-httpd-php'], decode, rawComponent },
] }];
