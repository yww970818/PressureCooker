import java.io.File;
import org.apache.jmeter.save.SaveService;
import org.apache.jmeter.util.JMeterUtils;
// Deserialization only. No engine, test lifecycle or samplers are started.
public final class VerifyNative {
 public static void main(String[] args)throws Exception {
  JMeterUtils.setJMeterHome(args[0]);JMeterUtils.loadJMeterProperties(args[0]+"/bin/jmeter.properties");
  File[] files=new File(args[1]).listFiles((dir,name)->name.endsWith(".jmx"));
  if(files==null)throw new IllegalArgumentException("missing fixtures");
  int failed=0;
  for(File file:files){try{SaveService.loadTree(file);}catch(Throwable e){failed++;System.err.println(file.getName()+": "+e);}}
  if(failed>0)throw new IllegalStateException(failed+" native XML fixtures failed");
  System.out.println("Deserialized "+files.length+" native component fixtures without execution");
 }
}
