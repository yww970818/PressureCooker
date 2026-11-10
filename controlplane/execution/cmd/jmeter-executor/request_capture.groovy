import groovy.json.JsonOutput
import groovy.json.JsonSlurper
import java.nio.charset.StandardCharsets
import java.net.URLDecoder
import java.net.URLEncoder
import org.apache.jmeter.samplers.SampleEvent

// No payload or exception text is logged. A complete oversize field is omitted
// rather than clipping an unmasked secret prefix. This listener is cached.
class PressureCookerPayloadMaskV1 {
    static boolean sensitive(Object key) {
        key != null && key.toString() ==~ /(?i).*(?:password|passwd|pwd|token|secret|api[-_.]?key|authorization|cookie|credential|session|private[_-]?key).*/
    }
    static Object walk(Object value, Collection<String> secrets) {
        if (value instanceof Map) {
            def out = [:]
            value.each { k,v -> out[k] = sensitive(k) ? '[REDACTED]' : walk(v,secrets) }
            return out
        }
        if (value instanceof List) return value.collect { walk(it,secrets) }
        if (value instanceof String) return plain(value,secrets)
        return value
    }
    static String plain(String text, Collection<String> secrets) {
        def out = text
        secrets.findAll { it != null && !it.isEmpty() }.unique().sort { a,b -> b.length() <=> a.length() }.each { secret ->
            out = out.replace(secret,'[REDACTED]')
            out = out.replace(URLEncoder.encode(secret,'UTF-8'),'[REDACTED]')
            out = out.replace(URLEncoder.encode(secret,'UTF-8').replace('+','%20'),'[REDACTED]')
        }
        out = out.replaceAll(/(?m)(^|[?&\s])([A-Za-z0-9_%+.-]+)\s*=\s*([^\r\n&;]*)/) { whole,separator,key,value ->
            def decoded=key
            try { decoded=URLDecoder.decode(key,'UTF-8') } catch(Exception ignored) {}
            sensitive(decoded) ? separator+key+'=[REDACTED]' : whole
        }
        out = out.replaceAll(/(?is)(<((?:[\w.-]+:)?[\w.-]*(?:password|passwd|pwd|token|secret|api[-_.]?key|credential|session)[\w.-]*)\b[^>]*>).*?(<\/\2\s*>|$)/, '$1[REDACTED]$3')
        out = out.replaceAll(/(?im)(\b(?:authorization|cookie|set-cookie)\s*[:=]\s*)[^\r\n]*/, '$1[REDACTED]')
        out = out.replaceAll(/(?is)(["']?(?:[\w.-]*(?:password|passwd|pwd|token|secret|api[-_.]?key|credential|session)[\w.-]*)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\{[\s\S]*|\[(?!REDACTED\])[\s\S]*|[^\s&,;\r\n}]+)/) { whole,prefix -> prefix + (whole.substring(prefix.length()).startsWith('"') ? '"[REDACTED]"' : '[REDACTED]') }
        out = out.replaceAll(/(?i)((?:https?|wss?):\/\/)[^\/?#@\s]+@/,'$1[REDACTED]@')
        return out
    }
    static String text(String value, Collection<String> secrets) {
        // Mask decoded JSON values before serialization; never run a broad
        // form/header expression across freshly serialized JSON delimiters.
        def out=new StringBuilder(), outside=new StringBuilder()
        int start=-1, depth=0; boolean quoted=false, escaped=false
        for(int i=0;i<value.length();i++) {
            char c=value.charAt(i)
            if(start<0) {
                if(c=='{' || c=='[') {
                    out.append(plain(outside.toString(),secrets));outside.setLength(0)
                    start=i;depth=1;quoted=false;escaped=false
                } else outside.append(c)
                continue
            }
            if(quoted) {if(escaped) escaped=false;else if(c=='\\') escaped=true;else if(c=='"') quoted=false}
            else if(c=='"') quoted=true
            else if(c=='{' || c=='[') depth++
            else if(c=='}' || c==']') depth--
            if(depth==0) {
                def block=value.substring(start,i+1)
                try {out.append(JsonOutput.toJson(walk(new JsonSlurper().parseText(block),secrets)))}
                catch(Exception ignored) {out.append(plain(block,secrets))}
                start=-1
            }
        }
        if(start>=0) out.append(plain(value.substring(start),secrets))
        out.append(plain(outside.toString(),secrets))
        return out.toString()
    }
    static String url(String value, Collection<String> secrets) {
        int question=value.indexOf('?')
        if(question>=0) {
            def head=value.substring(0,question), tail=value.substring(question+1)
            int fragment=tail.indexOf('#');if(fragment>=0) tail=tail.substring(0,fragment)
            tail=tail.split('&',-1).collect { entry ->
                def pair=entry.split('=',2)
                String key=pair[0]
                try {key=URLDecoder.decode(key,'UTF-8')} catch(Exception ignored) {}
                sensitive(key) ? pair[0]+'=[REDACTED]' : entry
            }.join('&')
            value=head+'?'+tail
        }
        int fragment=value.indexOf('#');if(fragment>=0) value=value.substring(0,fragment)+'#[REDACTED]'
        return plain(value,secrets)
    }
    static String cap(Object raw,int limit,Collection<String> secrets,Map flags,boolean isUrl=false) {
        if(raw==null) return null
        def value=raw.toString()
        if(value.length()>1048576) {flags.truncated=true;return null}
        value=isUrl ? url(value,secrets) : text(value,secrets)
        byte[] bytes=value.getBytes(StandardCharsets.UTF_8)
        if(bytes.length<=limit) return value
        flags.truncated=true
        int end=limit
        while(end>0 && (bytes[end]&0xC0)==0x80) end--
        return new String(bytes,0,end,StandardCharsets.UTF_8)
    }
}

try {
    if(args.length!=3) return
    def task=args[0], attempt=args[1], owner=args[2]
    def key='pressurecooker.request-samples.'+task+'.'+attempt+'.'+owner
    synchronized(props) {
        def state=props.get(key)
        if(state==null) {
            // HotSpot may change cwd while initializing perfdata when the
            // isolated engine has search-only directory access. Never derive
            // trusted capture paths from cwd, user.dir or JMeter FileServer.
            def tasksRoot=System.getProperty('pressurecooker.capture.tasks_root')
            if(tasksRoot==null || !new File(tasksRoot).isAbsolute()) return
            def dir=new File(tasksRoot,task+'/request-samples/'+attempt)
            def binding=new JsonSlurper().parse(new File(dir,'binding.json'),'UTF-8')
            if(binding.task_id!=task || binding.attempt_id!=attempt || binding.owner_key!=owner || binding.generation<=0 || binding.file.contains('/') || binding.file.contains('\\')) return
            def file=new File(dir,binding.file)
            def data=new JsonSlurper().parse(file,'UTF-8')
            if(data.task_id!=task || data.attempt_id!=attempt || data.owner_key!=owner || data.generation!=binding.generation) return
            state=[file:file,data:data,seen:new HashSet<String>(),full:false]
            props.put(key,state)
        }
        def result=sampleResult
        def label=result.getSampleLabel() ?: ''
        def kind='unknown'
        for(int i=0;i<SampleEvent.getVarCount();i++) {
            if(SampleEvent.getVarName(i) in ['sample_kind','sampleKind']) {
                def value=sampleEvent.getVarValue(i)?.toLowerCase()
                if(value in ['ordinary','ordinary_samples','sample','http']) kind='ordinary'
                if(value in ['transaction','transaction_sample']) kind='transaction'
            }
        }
        if(result.getResponseMessage()?.trim()?.toLowerCase()?.startsWith('number of samples in transaction :')) kind='transaction'
        def sampleKey=kind+'\u0000'+label+'\u0000'+result.isSuccessful()
        if(state.seen.contains(sampleKey)) return
        if(state.full) return
        def write={ -> state.file.withWriter('UTF-8') { it.write(JsonOutput.toJson(state.data)) } }
        if(state.data.samples.size()>=64 || label.getBytes('UTF-8').length>4096) {state.data.truncated=true;state.full=true;write();return}
        def secrets=[]
        props.each { k,v -> if((PressureCookerPayloadMaskV1.sensitive(k) || k.toString().startsWith('script_identity.') || k.toString().startsWith('jmeter.env.var.') || k.toString().startsWith('jmeter.env.api.') || k.toString().startsWith('jmeter.env.scalar.')) && v instanceof String) secrets.add(v) }
        vars.getIterator().each { entry -> if(PressureCookerPayloadMaskV1.sensitive(entry.key) && entry.value instanceof String) secrets.add(entry.value) }
        def headers=result.getRequestHeaders() ?: ''
        (headers =~ /(?im)^\s*(?:Authorization|Cookie|Set-Cookie)\s*:\s*([^\r\n]*)/).each { match ->
            secrets.add(match[1]);secrets.add(match[1].replaceFirst(/(?i)^(?:Bearer|Basic)\s+/,''))
            match[1].split(';').each { cookie -> int eq=cookie.indexOf('=');if(eq>=0) secrets.add(cookie.substring(eq+1).trim()) }
        }
        def flags=[truncated:false]
        def method=null
        if(result.metaClass.respondsTo(result,'getHTTPMethod')) method=result.getHTTPMethod()
        def params=result.getSamplerData()
        if(params==null || params.isEmpty()) {
            def actualQuery=null
            if(result.metaClass.respondsTo(result,'getQueryString')) actualQuery=result.getQueryString()
            if(actualQuery==null || actualQuery.isEmpty()) actualQuery=result.getURL()?.getQuery()
            if(actualQuery!=null && !actualQuery.isEmpty()) params=PressureCookerPayloadMaskV1.url('https://capture.invalid/?'+actualQuery,secrets).substring('https://capture.invalid/?'.length())
        }
        def body=null
        if(result.getResponseData()==null) body=null
        else if(result.getResponseData().length>1048576) flags.truncated=true
        else body=result.getResponseDataAsString()
        def assertions=[];def rawAssertions=result.getAssertionResults()
        rawAssertions.take(16).each { assertion -> assertions.add([name:PressureCookerPayloadMaskV1.cap(assertion.getName() ?: '',128,secrets,flags),failure:assertion.isFailure(),error:assertion.isError(),message:PressureCookerPayloadMaskV1.cap(assertion.getFailureMessage() ?: '',512,secrets,flags)]) }
        if(rawAssertions.length>16) flags.truncated=true
        def sample=[label:label,sample_kind:kind,timestamp_ms:result.getStartTime(),elapsed_ms:result.getTime(),success:result.isSuccessful(),method:PressureCookerPayloadMaskV1.cap(method,32,secrets,flags),url:PressureCookerPayloadMaskV1.cap(result.getURL()?.toString(),4096,secrets,flags,true),request_params:PressureCookerPayloadMaskV1.cap(params,4096,secrets,flags),response_code:PressureCookerPayloadMaskV1.cap(result.getResponseCode(),256,secrets,flags),response_body:PressureCookerPayloadMaskV1.cap(body,8192,secrets,flags),assertions_captured:true,assertions:assertions,truncated:flags.truncated]
        // Count encoded bytes, not characters; leave envelope overhead separate.
        def candidate=state.data.samples+[sample]
        if(JsonOutput.toJson(candidate).getBytes('UTF-8').length>131072) {state.data.truncated=true;state.full=true;write();return}
        state.data.samples=candidate;state.data.truncated=state.data.truncated||flags.truncated;state.seen.add(sampleKey);write()
    }
} catch(Exception ignored) {
    // Missing/unreadable capture is disclosed by the supervisor; never log data.
}
