const tags = new Set(['stringProp','boolProp','intProp','longProp','doubleProp','floatProp','FloatProperty','collectionProp','elementProp','mapProp','objProp']);
const propertyName=el=>el.getAttribute('name')||(['objProp','doubleProp','FloatProperty'].includes(el.tagName)?[...el.children].find(c=>c.tagName==='name')?.textContent:'')||'';
const propertyValue=el=>['doubleProp','FloatProperty'].includes(el.tagName)&&el.children.length?[...el.children].find(c=>c.tagName==='value')?.textContent||'':el.textContent;
export function parseProperties(xml = '') {
 if (typeof xml !== 'string' || /<!DOCTYPE|<!ENTITY/i.test(xml)) throw new Error('属性 XML 无效');
 const doc = new DOMParser().parseFromString(`<properties>${xml}</properties>`, 'application/xml');
 if (doc.querySelector('parsererror') || doc.documentElement.tagName!=='properties') throw new Error('属性 XML 无法解析');
 for(const child of doc.documentElement.children) if(!tags.has(child.tagName)||!propertyName(child).trim())throw new Error('只接受带名称的 JMeter 属性');
 return doc.documentElement;
}
export function propertyXML(node) {
 if(typeof node?.properties?.config?.properties_xml === 'string')return node.properties.config.properties_xml;
 const raw=node?.properties?.raw_xml;
 if(!raw)return '';
 if(/<!DOCTYPE|<!ENTITY/i.test(raw))throw new Error('导入 XML 无效');
 const doc=new DOMParser().parseFromString(`<root>${raw}</root>`,'application/xml');
 if(doc.querySelector('parsererror'))throw new Error('导入 XML 无法解析');
 const el=doc.documentElement.firstElementChild;
 return el ? [...el.childNodes].map(c=>new XMLSerializer().serializeToString(c)).join('') : '';
}
export function scalarProperties(xml){const out=Object.create(null);for(const el of parseProperties(xml).children)if(!el.children.length||['doubleProp','FloatProperty'].includes(el.tagName))out[propertyName(el)]=propertyValue(el);return out;}
export function serializeProperties(root){return [...root.childNodes].map(c=>new XMLSerializer().serializeToString(c)).join('');}
export function propertyRows(root,path=[]){return [...root.children].map((el,index)=>({path:[...path,index],tag:el.tagName,name:propertyName(el),elementType:el.getAttribute('elementType')||'',value:el.tagName==='objProp'?new XMLSerializer().serializeToString(el):propertyValue(el),children:propertyRows(el,[...path,index]),opaque:el.tagName==='objProp'}));}
export function updateProperty(xml,path,change){
 const root=parseProperties(xml);let el=root;
 for(const i of path){el=el.children[i];if(!el)throw new Error('属性不存在，请重新选择节点');}
 if(change.raw!==undefined){const fragment=parseProperties(change.raw);if(fragment.children.length!==1||fragment.firstElementChild.tagName!==el.tagName)throw new Error('对象属性 XML 类型必须保持不变');el.replaceWith(root.ownerDocument.importNode(fragment.firstElementChild,true));return serializeProperties(root);}
 if(change.remove){el.remove();return serializeProperties(root);}
 if(change.value!==undefined){if(['doubleProp','FloatProperty'].includes(el.tagName)&&el.children.length)[...el.children].find(c=>c.tagName==='value').textContent=String(change.value);else el.textContent=String(change.value);}
 for(const key of ['name','elementType'])if(change[key]!==undefined)el.setAttribute(key,change[key]);
 if(change.add){const next=root.ownerDocument.createElement(change.add.tag);next.setAttribute('name',change.add.name);if(next.tagName==='elementProp')next.setAttribute('elementType','Arguments');if(!['collectionProp','mapProp','elementProp'].includes(next.tagName))next.textContent=next.tagName==='boolProp'?'false':'';el.append(next);}
 return serializeProperties(root);
}
export function setNamedProperty(xml,name,value,tag='stringProp'){
 const root=parseProperties(xml);let el=[...root.children].find(c=>propertyName(c)===name);
 if(!el){el=root.ownerDocument.createElement(tag);el.setAttribute('name',name);root.append(el);}
 if(['doubleProp','FloatProperty'].includes(el.tagName)&&el.children.length){[...el.children].find(c=>c.tagName==='value').textContent=String(value);return serializeProperties(root);}
 if(el.children.length)throw new Error('适配字段只能修改标量属性');
 el.textContent=String(value);return serializeProperties(root);
}
export function namedProperty(xml,name,fallback=''){const el=[...parseProperties(xml).children].find(c=>propertyName(c)===name);return el?propertyValue(el):fallback;}
