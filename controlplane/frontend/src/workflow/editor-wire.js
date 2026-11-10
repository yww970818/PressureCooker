// Unpack only after the small envelope has crossed the Worker boundary. Each
// node retains its own objects; immutable source text can safely share strings.
export function decodeEditorPayload(value) {
  if (value?.format !== 'editor-text-v1') {
    if (typeof value?.format === 'string' && value.format.startsWith('editor-text-')) throw new Error('流程读取格式不受支持，请刷新后重试');
    return value;
  }
  const { data, texts, slots } = value;
  const invalid=()=>{throw new Error('流程参数传输不完整，请重新加载');};
  if (!data || typeof data !== 'object' || Array.isArray(data) || !Array.isArray(texts) || texts.some(text=>typeof text!=='string') || !Array.isArray(slots)) invalid();
  const paths=new Set();
  for (const slot of slots) {
    if (!Array.isArray(slot) || slot.length!==2) invalid();
    const [path,index]=slot;
    if (!Array.isArray(path) || !path.length || !Number.isInteger(index) || index<0 || index>=texts.length) invalid();
    const key=JSON.stringify(path);if(paths.has(key))invalid();paths.add(key);
    let target=data;
    for(let i=0;i<path.length;i++){
      const part=path[i];
      if(!target || typeof target!=='object' || (Array.isArray(target)?!Number.isInteger(part)||part<0||part>=target.length:typeof part!=='string') || !Object.hasOwn(target,part))invalid();
      if(i===path.length-1){if(target[part]!==null)invalid();Object.defineProperty(target,part,{value:texts[index],writable:true,enumerable:true,configurable:true});}
      else target=target[part];
    }
  }
  return data;
}
