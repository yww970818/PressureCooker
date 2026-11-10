import java.io.*;
import java.util.*;
import org.apache.jmeter.save.SaveService;
import org.apache.jmeter.testelement.TestElement;
import org.apache.jmeter.gui.JMeterGUIComponent;
import org.apache.jmeter.util.JMeterUtils;
public class NativeDefaults {
 public static void main(String[] args)throws Exception{
  JMeterUtils.setJMeterHome(args[0]);JMeterUtils.loadJMeterProperties(args[0]+"/bin/jmeter.properties");JMeterUtils.setLocale(Locale.ENGLISH);
  for(int i=1;i<args.length;i++){
   String[] parts=args[i].split(":",2);String alias=parts[0], name=SaveService.aliasToClass(alias);
   try{
    TestElement e=(TestElement)Class.forName(name).newInstance();
    // GUI defaults configure standard property names without loading any JMX or
    // starting an engine, connection, sampler or test lifecycle.
    String gui=parts.length>1?SaveService.aliasToClass(parts[1]):e.getPropertyAsString(TestElement.GUI_CLASS);
    if(gui.isEmpty())gui="org.apache.jmeter.testbeans.gui.TestBeanGUI";
    try {
     JMeterGUIComponent component;
     if(gui.endsWith("TestBeanGUI"))component=new org.apache.jmeter.testbeans.gui.TestBeanGUI(e.getClass());
     else component=(JMeterGUIComponent)Class.forName(gui).newInstance();
     component.clearGui();e=component.createTestElement();
    }catch(Throwable ignored){}
    e.setProperty(TestElement.TEST_CLASS,name);e.setName(alias);
    File dir=new File(args[0]+"/native-defaults");dir.mkdirs();try(FileOutputStream out=new FileOutputStream(new File(dir,alias+".xml"))){SaveService.saveElement(e,out);}
   }catch(Throwable error){System.err.println(alias+": "+error.getClass().getSimpleName());}
  }
 }
}
