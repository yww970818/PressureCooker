import { expect, it } from 'vitest';
import { wsVariables, renderWsText, resolveWsConfig } from './websocket-session.js';
it('merges project and collection variables without replacing the project address', () => {
 const vars=wsVariables({id:'qa',base_host:'https://qa.test',variables:{name:'project',projectOnly:'p'}},{project_environment_overrides:{qa:{variables:[{name:'name',value:'collection'},{name:'base_host',value:'wrong'}]}}});
 expect(vars).toMatchObject({base_host:'https://qa.test',name:'collection',projectOnly:'p'});
});
it('renders Socket.IO JSON with escaping and runtime numeric values', () => {
 const text='3:::'+JSON.stringify({sequence:'{{ser}}',data:{text:'{{text}}',nested:JSON.stringify({id:'{{result_id}}'})}});
 const result=JSON.parse(renderWsText(text,{ser:7,result_id:123,text:'a"b\nc'}).slice(4));
 expect(result.sequence).toBe(7);expect(result.data.text).toBe('a"b\nc');expect(JSON.parse(result.data.nested).id).toBe(123);
 expect(()=>renderWsText('{{missing}}',{})).toThrow('missing');
 const authored='  { "keep" : "wire whitespace" }  ';
 expect(renderWsText(authored,{})).toBe(authored);
});
it('resolves current unsaved WS settings and refuses non-websocket targets', () => {
 const config=resolveWsConfig({url:'wss://{{host}}/socket.io/1/websocket/{socket_id}',socketio:true,handshake_url:'https://{{host}}/socket.io/1/',headers:[{name:'Origin',value:'{{origin}}'}]},{host:'qa.test',origin:'https://hub.test'});
 expect(config.url).toContain('{socket_id}');expect(config.headers[0].value).toBe('https://hub.test');
 expect(()=>resolveWsConfig({url:'file:///etc/passwd'},{})).toThrow('WS');
});
