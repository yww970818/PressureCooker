import groovy.json.JsonOutput
import groovy.json.JsonSlurperClassic
import com.jayway.jsonpath.JsonPath
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.net.http.WebSocket
import java.nio.ByteBuffer
import java.nio.charset.StandardCharsets
import java.time.Duration
import java.util.concurrent.*
import java.util.concurrent.atomic.*
import java.util.regex.Pattern
import org.slf4j.LoggerFactory

// A stable native JMeter program. Authored data is Base64 JSON in sampler
// properties; no request or environment value is evaluated as program source.
class PCWorkflowWs {
    static final int MAX_MESSAGE = 262144
    static final int MAX_QUEUE = 1024
    static final int MAX_TRACE_EVENTS = 256
    static final def TRACE_LOG = LoggerFactory.getLogger('pressurecooker.workflow.ws')
    static final Set<String> AUTOMATIC=['ser','now','message_id','last_ser','socket_id'] as Set
    static final HttpClient CLIENT = HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1).connectTimeout(Duration.ofSeconds(15)).build()
    static final ScheduledExecutorService TIMER = Executors.newScheduledThreadPool(4, { Runnable r ->
        Thread t = new Thread(r, 'pressurecooker-ws-maintenance'); t.daemon = true; t
    } as ThreadFactory)
    static final ConcurrentMap<String, PCWsConnection> ACTIVE = new ConcurrentHashMap<>()
    static final Pattern TOKEN = Pattern.compile(/\$\{__P\(([^,()]+),([^()]*)\)\}|\{\{\s*([\p{L}\p{N}_.-]+)\s*\}\}|\$\{([\p{L}\p{N}_.-]+)\}/)
    static {
        TIMER.scheduleWithFixedDelay({
            ACTIVE.values().each { c ->
                if (!c.owner.alive) c.dispose(false)
            }
        } as Runnable, 1, 1, TimeUnit.SECONDS)
    }
    static Object parse(String s) {
        def items=new JsonSlurperClassic().parseText('['+s+']')
        if(items.size()!=1) throw new IllegalArgumentException('WS JSON 须为单个值')
        items[0]
    }
    static Object lookup(def m, Map values, Properties props) {
        String key = m.group(1) ?: m.group(3) ?: m.group(4)
        def value = m.group(1) ? props.getProperty(key) : values[key]
        if (value == null && !m.group(1) && !AUTOMATIC.contains(key)) value = props.getProperty(key)
        if (value == null && m.group(1) && m.group(2)) value = m.group(2)
        if (value == null) throw new IllegalArgumentException('缺少 WS 变量：' + key)
        String text = value.toString()
        if (TOKEN.matcher(text).find()) throw new IllegalArgumentException('WS 变量尚未填写：' + key)
        return text
    }
    static String expand(String source, Map values, Properties props) {
        def m = TOKEN.matcher(source ?: '')
        StringBuffer b = new StringBuffer()
        while (m.find()) m.appendReplacement(b, java.util.regex.Matcher.quoteReplacement(lookup(m, values, props).toString()))
        m.appendTail(b); b.toString()
    }
    // Parse before interpolating values. Nested JSON strings receive their own
    // encoding, including quotes and newlines.
    static Object interpolate(Object item, Map values, Properties props,boolean autoTypes) {
        if (item instanceof Map) return item.collectEntries { k,v -> [(expand(k.toString(),values,props)):interpolate(v,values,props,autoTypes)] }
        if (item instanceof List) return item.collect { interpolate(it,values,props,autoTypes) }
        if (!(item instanceof String)) return item
        String s = item
        if (((s.trim().startsWith('{') && !s.trim().startsWith('{{')) || s.trim().startsWith('[')) && TOKEN.matcher(s).find()) return message(s,values,props,autoTypes)
        def exact=TOKEN.matcher(s)
        if(autoTypes && exact.matches() && ['ser','now','last_ser'].contains(exact.group(3) ?: exact.group(4))) {
            String value=lookup(exact,values,props).toString()
            if(value.matches('[0-9]{1,18}')) return Long.parseLong(value)
        }
        expand(s,values,props)
    }
    static String message(String source, Map values, Properties props,boolean autoTypes=true) {
        if (!TOKEN.matcher(source ?: '').find()) return source ?: ''
        String prefix = source.startsWith('3:::') ? '3:::' : ''
        String json = prefix ? source.substring(4) : source
        if (!(json.trim().startsWith('{') || json.trim().startsWith('['))) return expand(source,values,props)
        // Protect bare numeric expressions while leaving authored JSON strings
        // intact. A private marker cannot be confused with user JSON content.
        Map bare = [:]
        StringBuilder prepared = new StringBuilder()
        boolean quoted = false, escape = false
        int pos = 0
        def m = TOKEN.matcher(json)
        while (m.find()) {
            String before = json.substring(pos,m.start())
            for (int i=0;i<before.length();i++) {
                char ch=before.charAt(i)
                if (escape) { escape=false; continue }
                if (ch == '\\' && quoted) escape=true
                else if (ch == '"') quoted=!quoted
            }
            prepared.append(before)
            if (quoted) prepared.append(m.group())
            else {
                String marker='__pc_ws_bare_' + UUID.randomUUID().toString()
                String value=lookup(m,values,props).toString()
                try { bare[marker]=parse(value) } catch (Exception ignored) { bare[marker]=value }
                prepared.append(JsonOutput.toJson(marker))
            }
            pos=m.end()
        }
        prepared.append(json.substring(pos))
        Object parsed=interpolate(parse(prepared.toString()),values,props,autoTypes)
        Closure restore
        restore = { v ->
            if (v instanceof Map) return v.collectEntries { k,x -> [(k):restore(x)] }
            if (v instanceof List) return v.collect { restore(it) }
            if (v instanceof String && bare.containsKey(v)) return bare[v]
            v
        }
        prefix + JsonOutput.toJson(restore(parsed))
    }
    static long interval(String s, Map values, Properties props,long maximum=120000L) {
        long n=Long.parseLong(expand(s,values,props))
        if (n<1 || n>maximum) throw new IllegalArgumentException('WS 超时或心跳间隔超出范围')
        n
    }
    static Map snapshot(def vars) {
        Map out=[:]
        def it=vars.getIterator()
        while (it.hasNext()) { def e=it.next(); if (e.value instanceof String) out[e.key]=e.value }
        out
    }
    static Map matcher(Map config, Map values, Properties props) {
        String mode=config.receiveMode
        Map out=[mode:mode]
        if (mode=='regex') out.pattern=Pattern.compile(expand(config.matchText,values,props))
        if (mode=='contains') out.text=expand(config.matchText,values,props)
        if (mode=='json') {
            out.rules=parse(message(config.matchJson,values,props,false))
            if (!(out.rules instanceof Map) || out.rules.isEmpty()) throw new IllegalArgumentException('JSON 匹配至少需要一个 JSONPath 条件')
        }
        out
    }
    static boolean matches(Map entry,Map rule) {
        switch (rule.mode) {
            case 'contains': return entry.raw.contains(rule.text)
            case 'regex': return rule.pattern.matcher(entry.raw).find()
            case 'json':
                return rule.rules.every { k,v ->
                    try {
                        def actual=JsonPath.read(JsonOutput.toJson(entry),k.toString())
                        return actual==v
                    } catch (Exception ignored) { return false }
                }
            default: return true
        }
    }
}

class PCWsConnection implements WebSocket.Listener {
    final Thread owner=Thread.currentThread()
    final AtomicLong sequence=new AtomicLong()
    final AtomicLong serial=new AtomicLong()
    final AtomicLong queueBytes=new AtomicLong()
    final LinkedBlockingDeque<Map> inbox=new LinkedBlockingDeque<>(PCWorkflowWs.MAX_QUEUE)
    final Map<String,String> runtimeValues=new ConcurrentHashMap<>()
    final AtomicReference<String> failure=new AtomicReference<>()
    final AtomicInteger traceCount=new AtomicInteger()
    final String traceId=UUID.randomUUID().toString().replace('-','')
    final long traceStart=System.nanoTime()
    final boolean traceEnabled
    final Object sendLock=new Object()
    final Object queueLock=new Object()
    final String registryKey
    final boolean socketio
    volatile long timeout
    final Properties props
    volatile Map variables=[:]
    volatile Map lastSent=[:]
    volatile WebSocket socket
    volatile boolean closed=false
    volatile ScheduledFuture heartbeat
    final StringBuilder fragments=new StringBuilder()
    final ByteArrayOutputStream binary=new ByteArrayOutputStream()
    PCWsConnection(String key,boolean socketio,long timeout,Properties props) { this.registryKey=key; this.socketio=socketio; this.timeout=timeout; this.props=props; this.traceEnabled=props.getProperty('websocket_trace_events','false')=='true' }
    // Opt-in diagnostics include only transport counters and generated IDs.
    // Frames, authored matching rules and user values never enter the trace.
    void trace(String phase,Map metadata=[:]) {
        if(!traceEnabled) return
        int count=traceCount.incrementAndGet()
        if(count>PCWorkflowWs.MAX_TRACE_EVENTS+1) return
        Map record=[trace_id:traceId,time_ms:System.currentTimeMillis(),elapsed_ms:TimeUnit.NANOSECONDS.toMillis(System.nanoTime()-traceStart),phase:phase]
        if(count>PCWorkflowWs.MAX_TRACE_EVENTS) record.phase='trace_limit'
        else {
            ['ser','sequence','pending','boundary'].each { k ->
                def value=metadata[k]
                if(value!=null && value.toString().matches('-?[0-9]{1,18}')) record[k]=Long.parseLong(value.toString())
            }
            ['message_id'].each { k ->
                def value=metadata[k]
                if(value instanceof String && value.matches('[A-Za-z0-9_.#-]{1,160}')) record[k]=value
            }
        }
        PCWorkflowWs.TRACE_LOG.info('PC_WS_EVENT '+JsonOutput.toJson(record))
    }
    void traceSend(long seq,String messageId) {
        trace('send',[ser:seq,message_id:messageId])
    }
    Map values(boolean increment) {
        Map out=new HashMap(variables)
        PCWorkflowWs.AUTOMATIC.each { out.remove(it) }
        out.putAll(lastSent)
        out.putAll(runtimeValues)
        if (increment) {
            out.ser=serial.incrementAndGet().toString()
            out.now=System.currentTimeMillis().toString()
            out.message_id=UUID.randomUUID().toString().replace('-','')
        }
        out
    }
    void check() {
        if (failure.get()) throw new IOException(failure.get())
        if (closed) throw new IOException('WS 连接已关闭')
    }
    void write(String text) {
        check()
        if (text.getBytes(StandardCharsets.UTF_8).length>PCWorkflowWs.MAX_MESSAGE) throw new IllegalArgumentException('WS 消息超过 256 KiB')
        synchronized(sendLock) { socket.sendText(text,true).get(timeout,TimeUnit.MILLISECONDS) }
    }
    long send(String template, boolean internal) {
        Map data=values(true)
        long seq=Long.parseLong(data.ser)
        if (!internal) { runtimeValues.last_ser=data.ser; lastSent=[ser:data.ser,now:data.now,message_id:data.message_id] }
        String text=PCWorkflowWs.message(template,data,props)
        if(!internal) traceSend(seq,data.message_id)
        write(text)
        seq
    }
    void enqueue(Map data) {
        data._sequence=sequence.incrementAndGet()
        data._bytes=data.raw.getBytes(StandardCharsets.UTF_8).length
        synchronized(queueLock) {
            if (queueBytes.get()+data._bytes>8388608 || !inbox.offerLast(data)) { failure.compareAndSet(null,'WS 接收队列超过 1024 条或 8 MiB'); dispose(false) }
            else {
                queueBytes.addAndGet(data._bytes as long)
                if(traceEnabled) trace('enqueue',[sequence:data._sequence])
            }
        }
    }
    void accept(String raw) {
        if (socketio && raw=='2::') { write('2::'); return }
        if (socketio && raw=='1::') return
        Object frame=null
        try { frame=PCWorkflowWs.parse(socketio && raw.startsWith('3:::') ? raw.substring(4) : raw) } catch (Exception ignored) { }
        enqueue([raw:raw,frame:frame])
    }

    void onOpen(WebSocket ws) { socket=ws; if(closed) ws.abort(); else ws.request(1) }
    CompletionStage<?> onText(WebSocket ws,CharSequence data,boolean last) {
        try {
            if (fragments.length()+data.length()>PCWorkflowWs.MAX_MESSAGE) throw new IOException('WS 消息超过 256 KiB')
            fragments.append(data)
            if (last) { String text=fragments.toString(); fragments.setLength(0); if(text.getBytes(StandardCharsets.UTF_8).length>PCWorkflowWs.MAX_MESSAGE) throw new IOException('WS 消息超过 256 KiB'); accept(text) }
        } catch(Exception e) { failure.compareAndSet(null,'WS 接收失败：'+e.class.simpleName); dispose(false) }
        ws.request(1); CompletableFuture.completedFuture(null)
    }
    CompletionStage<?> onBinary(WebSocket ws,ByteBuffer data,boolean last) {
        try {
            if (binary.size()+data.remaining()>PCWorkflowWs.MAX_MESSAGE) throw new IOException('WS 二进制消息超过 256 KiB')
            byte[] bytes=new byte[data.remaining()]; data.get(bytes); binary.write(bytes)
            if(last) { enqueue([raw:Base64.encoder.encodeToString(binary.toByteArray()),frame:null,binary:true]); binary.reset() }
        } catch(Exception e) { failure.compareAndSet(null,'WS 二进制接收失败'); dispose(false) }
        ws.request(1); CompletableFuture.completedFuture(null)
    }
    CompletionStage<?> onClose(WebSocket ws,int statusCode,String reason) { closed=true; runtimeValues.remove('socket_id'); if(heartbeat) heartbeat.cancel(false); PCWorkflowWs.ACTIVE.remove(registryKey,this); CompletableFuture.completedFuture(null) }
    void onError(WebSocket ws,Throwable error) { failure.compareAndSet(null,'WS 传输失败：'+error.class.simpleName); dispose(false) }
    Map receive(Map rule,long boundary,long wait) {
        long deadline=System.nanoTime()+TimeUnit.MILLISECONDS.toNanos(wait)
        if(traceEnabled) trace('wait',[boundary:boundary,pending:inbox.size()])
        while(true) {
            // Search without removing unmatched asynchronous notifications. This
            // preserves order and the original queue bound while waiting.
            synchronized(queueLock) {
                def it=inbox.iterator()
                while(it.hasNext()) {
                    Map entry=it.next()
                    if (entry._sequence>boundary && PCWorkflowWs.matches(entry,rule)) {
                        if(traceEnabled) trace('match',[sequence:entry._sequence])
                        it.remove(); queueBytes.addAndGet(-(entry._bytes as long)); Map result=new LinkedHashMap(entry); result.remove('_sequence'); result.remove('_bytes'); return result
                    }
                }
            }
            check()
            long left=deadline-System.nanoTime()
            if(left<=0) {
                if(traceEnabled) trace('timeout',[boundary:boundary,pending:inbox.size()])
                throw new TimeoutException('WS 接收等待超时')
            }
            Thread.sleep(Math.min(20,Math.max(1,TimeUnit.NANOSECONDS.toMillis(left))))
        }
    }
    void startHeartbeat(Map config) {
        if (!config.heartbeatEnabled) return
        long every=PCWorkflowWs.interval(config.heartbeatIntervalMs,values(false),props,300000L)
        String template=config.heartbeatMessage
        if (!template) throw new IllegalArgumentException('启用定时心跳时必须填写心跳消息')
        heartbeat=PCWorkflowWs.TIMER.scheduleWithFixedDelay({
            try {
                if(!this.owner.alive || closed) { dispose(false); return }
                send(template,true)
            } catch(Exception e) { failure.compareAndSet(null,'WS 定时心跳失败：'+e.class.simpleName); dispose(false) }
        } as Runnable,every,every,TimeUnit.MILLISECONDS)
    }
    void dispose(boolean graceful) {
        closed=true
        runtimeValues.remove('socket_id')
        if(heartbeat) heartbeat.cancel(false)
        PCWorkflowWs.ACTIVE.remove(registryKey,this)
        if(socket!=null) {
            try { if(graceful) { synchronized(sendLock) { socket.sendClose(WebSocket.NORMAL_CLOSURE,'workflow close').get(Math.min(timeout,2000),TimeUnit.MILLISECONDS) } } } catch(Exception ignored) { }
            try { socket.abort() } catch(Exception ignored) { }
        }
        synchronized(queueLock) { inbox.clear(); queueBytes.set(0) }
    }
}

Map config=PCWorkflowWs.parse(new String(Base64.decoder.decode(sampler.getPropertyAsString('pc.websocket.config')),StandardCharsets.UTF_8))
Map current=PCWorkflowWs.snapshot(vars)
PCWorkflowWs.AUTOMATIC.each { current.remove(it) }
String action=config.action
String id=''
PCWsConnection connection=null
try {
    id=PCWorkflowWs.expand(config.connectionId,current,props)
    String key=Thread.currentThread().id.toString()+':'+id
    long timeout=PCWorkflowWs.interval(config.timeoutMs,current,props)
    Map connections=vars.getObject('pc.websocket.connections')
    if (connections==null) { connections=[:]; vars.putObject('pc.websocket.connections',connections) }
    connection=connections[id]
    Object result
    if(action=='connect') {
        if(connection==null && connections.size()>=128) throw new IllegalArgumentException('WS 单线程连接数超过 128')
        if(connection!=null) connection.dispose(true)
        connection=new PCWsConnection(key,config.socketio as boolean,timeout,props)
        connection.variables=current
        String url=PCWorkflowWs.expand(config.url,current,props)
        List headers=config.headers.findAll { it.enabled!=false && it.name }.collect { [name:PCWorkflowWs.expand(it.name,current,props),value:PCWorkflowWs.expand(it.value,current,props)] }
        if(config.socketio) {
            String endpoint=PCWorkflowWs.expand(config.handshakeUrl,current,props)
            URI handshake=URI.create(endpoint)
            if(!(handshake.scheme in ['http','https'])) throw new IllegalArgumentException('Socket.IO 握手地址必须为 HTTP(S)')
            def request=HttpRequest.newBuilder(handshake).timeout(Duration.ofMillis(timeout)).GET()
            headers.each { request.header(it.name,it.value) }
            def response=PCWorkflowWs.CLIENT.send(request.build(),HttpResponse.BodyHandlers.ofString())
            String body=response.body()
            String[] fields=body.split(':')
            if(response.statusCode()!=200 || fields.length!=4 || !fields[0].matches('[A-Za-z0-9_-]{1,200}') || !(fields[3].split(',').contains('websocket')) || !url.contains('{socket_id}')) throw new IOException('Socket.IO 动态握手失败')
            connection.runtimeValues.socket_id=fields[0]
            url=url.replace('{socket_id}',fields[0])
        }
        URI target=URI.create(url)
        if(!(target.scheme in ['ws','wss'])) throw new IllegalArgumentException('WS 地址必须为 ws:// 或 wss://')
        def builder=PCWorkflowWs.CLIENT.newWebSocketBuilder().connectTimeout(Duration.ofMillis(timeout))
        headers.each { builder.header(it.name,it.value) }
        List protocols=PCWorkflowWs.expand(config.protocols,current,props).split(',').collect{it.trim()}.findAll{it}
        if(protocols) builder.subprotocols(protocols[0],protocols.drop(1) as String[])
        connection.socket=builder.buildAsync(target,connection).get(timeout,TimeUnit.MILLISECONDS)
        connection.check()
        connections[id]=connection
        PCWorkflowWs.ACTIVE[key]=connection
        connection.startHeartbeat(config)
        result=[connected:true,connection_id:id,protocol:connection.socket.subprotocol,socketio:config.socketio]
        if(config.socketio) result.socket_id=connection.runtimeValues.socket_id
    } else if(action=='close') {
        if(connection!=null) connection.dispose(true)
        connections.remove(id)
        result=[closed:true,connection_id:id]
    } else {
        if(connection==null) throw new IOException('WS 连接不存在，请先添加建连节点')
        connection.variables=current
        connection.timeout=timeout
        connection.check()
        if(action=='send' || action=='request') {
            long boundary=connection.sequence.get()
            long serial=connection.send(config.message,false)
            result=action=='request' ? connection.receive(PCWorkflowWs.matcher(config,connection.values(false),props),boundary,timeout) : [sent:true,ser:serial,connection_id:id]
        } else if(action=='read') result=connection.receive(PCWorkflowWs.matcher(config,connection.values(false),props),-1L,timeout)
        else throw new IllegalArgumentException('不支持的 WS 操作')
    }
    vars.remove('socket_id')
    if(connection!=null) { connection.lastSent.each { k,v -> vars.put(k,v.toString()) }; connection.runtimeValues.each { k,v -> vars.put(k,v.toString()) } }
    SampleResult.setSuccessful(true)
    SampleResult.setResponseCode(action=='connect' ? '101' : '200')
    SampleResult.setResponseMessage('WS '+action+' OK')
    SampleResult.setDataType('text')
    SampleResult.setContentType('application/json')
    SampleResult.setResponseData(JsonOutput.toJson(result),'UTF-8')
} catch(Exception e) {
    // Authentication values, raw frames and endpoint queries never enter errors.
    String detail=(e instanceof IllegalArgumentException || e instanceof IOException || e instanceof TimeoutException) ? e.message : e.class.simpleName
    if(e instanceof IllegalArgumentException && !(detail?.startsWith('WS ') || detail?.startsWith('缺少 WS ') || detail?.startsWith('JSON 匹配') || detail?.startsWith('启用定时') || detail?.startsWith('Socket.IO '))) detail='WS 参数无效'
    vars.remove('socket_id')
    if(connection!=null) connection.dispose(false)
    def connections=vars.getObject('pc.websocket.connections'); if(connections!=null) connections.remove(id)
    SampleResult.setSuccessful(false)
    SampleResult.setResponseCode(e instanceof TimeoutException ? 'WS_TIMEOUT' : 'WS_ERROR')
    SampleResult.setResponseMessage('WS '+action+' 失败：'+detail)
    SampleResult.setDataType('text')
    SampleResult.setResponseData(JsonOutput.toJson([action:action,error:detail]),'UTF-8')
}
