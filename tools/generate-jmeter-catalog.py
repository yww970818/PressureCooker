#!/usr/bin/env python3
"""Rebuild the pinned native catalog using only official JMeter classes.
Run from repository root with a local JDK 8+ (javac and java on PATH).
This constructs GUI defaults in headless mode; it never starts a test engine.
"""
import io,json,re,tarfile,zipfile,struct,subprocess,tempfile,xml.etree.ElementTree as ET,hashlib
from pathlib import Path
classes={}
archive=Path('vendor/apache-jmeter-5.6.3.tgz')
expected=Path(str(archive)+'.sha512').read_text().split()[0]
if hashlib.sha512(archive.read_bytes()).hexdigest()!=expected:
    raise SystemExit('Pinned official JMeter SHA512 mismatch')
with tarfile.open('vendor/apache-jmeter-5.6.3.tgz') as tar:
    props=tar.extractfile('apache-jmeter-5.6.3/bin/saveservice.properties').read().decode()
    for member in tar.getmembers():
        if '/lib/ext/ApacheJMeter_' in member.name and member.name.endswith('.jar'):
            with zipfile.ZipFile(io.BytesIO(tar.extractfile(member).read())) as z:
                for n in z.namelist():
                    if n.endswith('.class'):
                        classes[n[:-6].replace('/','.')]=z.read(n)
def parents(data):
    p=8; count=struct.unpack_from('>H',data,p)[0];p+=2;cp=[None]*count;i=1
    while i<count:
        tag=data[p];p+=1
        if tag==1:
            n=struct.unpack_from('>H',data,p)[0];p+=2;cp[i]=data[p:p+n].decode('utf8','replace');p+=n
        elif tag==7:cp[i]=struct.unpack_from('>H',data,p)[0];p+=2
        elif tag in (3,4,9,10,11,12,17,18):p+=4
        elif tag in (5,6):p+=8;i+=1
        elif tag in (8,16,19,20):p+=2
        elif tag==15:p+=3
        else:raise ValueError(tag)
        i+=1
    flags,own,sup,n=struct.unpack_from('>HHHH',data,p);p+=8
    ids=[sup]+list(struct.unpack_from('>'+('H'*n),data,p))
    return flags,[cp[cp[x]].replace('/','.') for x in ids if x]
meta={k:parents(v) for k,v in classes.items()}
def ancestry(k,seen=None):
    seen=set() if seen is None else seen
    if k in seen:return seen
    seen.add(k)
    for p in meta.get(k,(0,[]))[1]:ancestry(p,seen)
    return seen
mappings={}
for line in props.splitlines():
    if not line or line.startswith(('#','_')) or '=' not in line:continue
    aliases,cls=line.split('=',1)
    for alias in aliases.split(','):mappings[alias]=cls
excluded={'Argument','Arguments','HTTPArgument','HTTPFileArg','HTTPFileArgs','Header','Cookie','JMSProperties','JMSProperty','LDAPArgument','LDAPArguments','SampleSaveConfiguration','TransactionSampler','RemoteListenerWrapper','RemoteSampleListenerWrapper','RemoteTestListenerWrapper','RemoteThreadsListenerWrapper','WorkBench','ReflectionThreadGroup','OpenModelThreadGroupController','MailerModel','SubstitutionElement','StaticHost','TestPlan'}
roles=[('org.apache.jmeter.threads.AbstractThreadGroup','thread'),('org.apache.jmeter.control.Controller','controller'),('org.apache.jmeter.samplers.Sampler','sampler'),('org.apache.jmeter.assertions.Assertion','assertion'),('org.apache.jmeter.processor.PreProcessor','preprocessor'),('org.apache.jmeter.processor.PostProcessor','postprocessor'),('org.apache.jmeter.timers.Timer','timer'),('org.apache.jmeter.config.ConfigElement','config'),('org.apache.jmeter.samplers.SampleListener','listener')]
titles={'JSR223Sampler':'JSR223 脚本采样器','JSR223Assertion':'JSR223 脚本断言','JSR223PreProcessor':'JSR223 前置处理器','JSR223PostProcessor':'JSR223 后置处理器','JSR223Timer':'JSR223 定时器','JSR223Listener':'JSR223 监听器','ConstantThroughputTimer':'固定吞吐量定时器','TestPlan':'测试计划','Arguments':'用户变量','ThreadGroup':'线程组','SetupThreadGroup':'准备线程组','PostThreadGroup':'收尾线程组','OpenModelThreadGroup':'开放模型线程组','WhileController':'While 循环控制器','GenericController':'简单控制器','OnceOnlyController':'仅一次控制器','ThroughputController':'吞吐量控制器','RegexExtractor':'正则表达式提取器','CookieManager':'HTTP Cookie 管理器','CacheManager':'HTTP 缓存管理器','DurationAssertion':'持续时间断言','SizeAssertion':'响应大小断言'}
items=[];seen=set()
for alias,cls in mappings.items():
    if cls in seen or cls not in meta or alias in excluded or alias.endswith('_') or meta[cls][0]&0x600:continue
    ancestors=ancestry(cls)
    if 'org.apache.jmeter.testelement.TestElement' not in ancestors:continue
    kind=next((r for base,r in roles if base in ancestors),None)
    if kind is None:continue
    gui=next((a for a,c in mappings.items() if c in classes and (a==alias+'Gui' or a==alias+'GUI')), 'TestBeanGUI')
    items.append(dict(component=alias,class_name=cls,title=titles.get(alias,alias),kind=kind,gui=gui,source='jmeter',version='5.6.3'))
    seen.add(cls)
for alias in ('TestPlan','Arguments'):
    items.append(dict(component=alias,class_name=mappings[alias],title=titles[alias],kind='plan' if alias=='TestPlan' else 'config',gui='TestPlanGui' if alias=='TestPlan' else 'ArgumentsPanel',source='jmeter',version='5.6.3'))
items.sort(key=lambda x:x['component'])
with tempfile.TemporaryDirectory(prefix='pressurecooker-native-') as temp:
    target=Path(temp)
    with tarfile.open(archive) as tar:
        tar.extractall(target,filter='data')
    home=target/'apache-jmeter-5.6.3'
    classpath=f'{home}/lib/*:{home}/lib/ext/*'
    subprocess.run(['javac','-cp',classpath,'-d',str(target),'tools/jmeter-catalog/NativeDefaults.java'],check=True)
    subprocess.run(['java','-Djava.awt.headless=true','-cp',f'{target}:{classpath}','NativeDefaults',str(home)]+[x['component']+':'+x['gui'] for x in items],check=True,timeout=60,capture_output=True)
    for item in items:
        element=ET.parse(home/'native-defaults'/(item['component']+'.xml')).getroot()
        item['template_properties']=''.join(ET.tostring(c,encoding='unicode') for c in element)
        item['gui']=element.attrib.get('guiclass',item['gui'])
Path('controlplane/manager-go/internal/workflowcatalog/native.json').write_text(json.dumps(items,ensure_ascii=False,indent=2)+'\n')
print(len(items),'native components');print(', '.join(x['component'] for x in items))
