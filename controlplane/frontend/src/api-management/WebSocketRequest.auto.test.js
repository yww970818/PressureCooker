// @vitest-environment jsdom
import {afterEach,expect,it,vi} from 'vitest';
import {mount,flushPromises} from '@vue/test-utils';
import WebSocketRequest from './WebSocketRequest.vue';
afterEach(()=>vi.unstubAllGlobals());
it('defers auto address preview without context traffic and resolves it only when connecting',async()=>{
 const sockets=[];class Socket{readyState=0;constructor(url){this.url=url;sockets.push(this);}close(){this.readyState=3;}}
 vi.stubGlobal('WebSocket',Socket);const fetcher=vi.fn(async()=>new Response(JSON.stringify({client_ip:'192.0.2.22',timestamp_ms:'1794180000000'})));vi.stubGlobal('fetch',fetcher);
 const document={openapi:'3.0.3',info:{title:'fixture',version:'1'},paths:{'/echo':{get:{summary:'Echo','x-pressure-ws':{url:'wss://test.invalid/?source={{source}}&time={{clock}}',messages:[]}}}}};
 const wrapper=mount(WebSocketRequest,{props:{document,request:{path:'/echo',method:'get'},mode:'direct',projectEnvironment:{id:'qa',base_host:'https://test.invalid',variables:[{name:'source',value:'{{$auto.client_ip}}'},{name:'clock',value:'{{$auto.timestamp_ms}}'}]}}});
 try{await flushPromises();expect(fetcher).not.toHaveBeenCalled();expect(wrapper.text()).toContain('连接时获取');expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  await wrapper.findAll('button').find(button=>button.text()==='连接').trigger('click');await flushPromises();expect(fetcher).toHaveBeenCalledTimes(1);expect(sockets[0].url).toContain('source=192.0.2.22&time=1794180000000');expect(JSON.stringify(wrapper.vm.snapshot())).not.toContain('192.0.2.22');
 }finally{wrapper.unmount();}
});
