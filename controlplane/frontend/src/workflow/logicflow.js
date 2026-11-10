import LogicFlow, { h, RectNode, RectNodeModel } from '@logicflow/core';
import '@logicflow/core/dist/index.css';
import { componentInfo } from './catalog.js';
import { createWindowedGraph } from './windowed-graph.js';
import { graphStamp,createGraphPayloadStore } from './graph-payloads.js';
import { canonicalGraph, connectionError } from './graph.js';

const MIN_ZOOM = .2, MAX_ZOOM = 2, ZOOM_STEP = .1, FIT_PADDING = 24;

// Keep the same line vocabulary as ui/Icon.vue, rendered directly in SVG so
// LogicFlow nodes do not depend on an icon font or a second UI renderer.
const nodeGlyphs = {
  TestPlan: [['path', { d: 'M14 2H5v20h14V7l-5-5Zm0 0v6h5M8 13h8M8 17h6' }]],
  ThreadGroup: [['circle', { cx: 12, cy: 7, r: 4 }], ['path', { d: 'M4 22v-3a8 8 0 0 1 16 0v3Z' }]],
  HTTPSamplerProxy: [['circle', { cx: 12, cy: 12, r: 9 }], ['path', { d: 'M3 12h18M12 3a17 17 0 0 1 0 18 17 17 0 0 1 0-18Z' }]],
  WebSocketSampler: [['path', { d: 'M3 8h17m-4-4 4 4-4 4M21 16H4m4-4-4 4 4 4' }]],
  Arguments: [['path', { d: 'm8 7-5 5 5 5m8-10 5 5-5 5m-3-13-2 22' }]],
  ResponseAssertion: [['path', { d: 'm5 12 4 4L19 6' }]],
  LoopController: [['path', { d: 'M20 7v5h-5M20 12a8 8 0 1 0-2 6' }]],
  IfController: [['rect', { x: 9, y: 2, width: 6, height: 5, rx: 1 }], ['rect', { x: 2, y: 17, width: 6, height: 5, rx: 1 }], ['rect', { x: 16, y: 17, width: 6, height: 5, rx: 1 }], ['path', { d: 'M12 7v5M5 17v-5h14v5' }]],
  TransactionController: [['path', { d: 'M3 7V5h7l2 3h9v12H3V7Z' }]],
  HeaderManager: [['rect', { x: 5, y: 5, width: 14, height: 17, rx: 2 }], ['path', { d: 'M9 5V3h6v2M9 11h6M9 15h4' }]],
  CSVDataSet: [['rect', { x: 3, y: 4, width: 18, height: 6, rx: 2 }], ['rect', { x: 3, y: 14, width: 18, height: 6, rx: 2 }], ['path', { d: 'M7 7h.01M7 17h.01M17 7h1M17 17h1' }]],
  ConstantTimer: [['path', { d: 'M3 4v5h5M3 9a9 9 0 1 1 0 6M12 7v5l3 2' }]],
  JSONPostProcessor: [['circle', { cx: 10, cy: 10, r: 6 }], ['path', { d: 'm15 15 5 5' }]],
};

export function renderableGraph(graph) {
  const data = canonicalGraph(graph);
  data.nodes = data.nodes.map(node => ({ ...node, text: { ...node.text, x: node.x, y: node.y } }));
  data.edges = data.edges.map(edge => ({ ...edge, text: { value: String((edge.properties.order ?? 0) + 1) } }));
  return data;
}

export function createLogicFlow(container, callbacks, options = {}) {
  let windowed;
 const payloads=createGraphPayloadStore();
 const authored=()=>windowed?.active?windowed.capture():payloads.restore(lf.getGraphRawData());
 const changed=()=>{if(windowed?.active){windowed.capture();return;}callbacks.change(authored(),{authored:true});};
 let readOnly = Boolean(options.readOnly), disabled = false;
  let palette = { surface: '#ffffff', text: '#27243c', muted: '#8a91a4', border: '#e9e8f2', accent: '#6c5ce7', accentSoft: '#efecff', canvas: '#fbfbfe', grid: '#dfddeb', dark: false, ...options.tokens };
  function tone(component) {
    const group = componentInfo(component).group;
    if (group === '配置与数据' || component === 'ThreadGroup') return { color: palette.dark ? '#7bdbbe' : '#20a58b', fill: palette.dark ? '#17362e' : '#edfaf5' };
    if (component === 'ResponseAssertion' || component === 'JSONPostProcessor') return { color: palette.dark ? '#ffbe91' : '#dc874a', fill: palette.dark ? '#3a2c24' : '#fff2e9' };
    return { color: palette.accent, fill: palette.accentSoft };
  }
  class JMeterNodeModel extends RectNodeModel {
    setAttributes() { this.width = 190; this.height = 74; this.radius = 8; this.text.editable = false; }
    getConnectedSourceRules() {
      return [{ message: '连接不符合父子树要求，请检查作用域、已有父级或循环', validate: (source, target, _a, _b, edgeId) => !connectionError(windowed?.active?windowed.graph:this.graphModel.modelToGraphData(), source.id, target.id, edgeId) }];
    }
    getNodeStyle() {
      const style = super.getNodeStyle();
      return { ...style, fill: this.properties.enabled === false ? palette.surface : tone(this.properties.component).fill, stroke: this.isSelected ? palette.accent : this.properties.component === 'TestPlan' ? palette.accent : palette.border, strokeWidth: this.isSelected ? 2 : 1 };
    }
  }
  class JMeterNode extends RectNode {
    getText() { return null; }
    getShape() {
      const { model } = this.props;
      const name = String(model.properties.name || '未命名组件');
      const title = componentInfo(model.properties.component).title;
      const color = model.properties.enabled === false ? palette.muted : tone(model.properties.component).color;
      return h('g', {}, [
        h('rect', { ...model.getNodeStyle(), x: model.x - model.width / 2, y: model.y - model.height / 2, width: model.width, height: model.height, rx: 11, ry: 11 }),
        h('circle', { cx: model.x - 66, cy: model.y - 3, r: 12, fill: palette.surface, stroke: color, strokeWidth: 1.4 }),
        h('g', { transform: `translate(${model.x - 75} ${model.y - 12}) scale(.75)`, stroke: color, strokeWidth: 1.7, strokeLinecap: 'round', strokeLinejoin: 'round', fill: 'none' }, (nodeGlyphs[model.properties.component] || nodeGlyphs.TestPlan).map(([tag, attributes]) => h(tag, attributes))),
        h('text', { x: model.x + 13, y: model.y - 1, textAnchor: 'middle', fill: palette.text, fontSize: 13, fontWeight: 550 }, name.length > 15 ? `${name.slice(0, 14)}…` : name),
        h('text', { x: model.x + 13, y: model.y + 18, textAnchor: 'middle', fill: color, fontSize: 10 }, `${title}${model.properties.enabled === false ? ' · 已停用' : ''}`),
      ]);
    }
  }
  const lf = new LogicFlow({
    partial: true,
    container, grid: { size: 20, visible: true, type: 'dot', config: { color: palette.grid, thickness: 1 } },
    background: { backgroundColor: palette.canvas }, edgeType: 'polyline', history: true, keyboard: { enabled: !readOnly }, isSilentMode: readOnly,
    adjustEdge: true, allowResize: false, allowRotate: false, nodeTextEdit: false, edgeTextEdit: false,
    nodeTextDraggable: false, edgeTextDraggable: false, stopScrollGraph: true,
    guards: { beforeDelete: data => data.properties?.component !== 'TestPlan' },
  });
  let viewportSize = container.getBoundingClientRect();
  lf.setZoomMiniSize(MIN_ZOOM); lf.setZoomMaxSize(MAX_ZOOM);
  lf.register({ type: 'jmeter-node', view: JMeterNode, model: JMeterNodeModel });
  function recolor(tokens) {
    palette = { ...palette, ...tokens };
    lf.setTheme({ rect: { fill: palette.surface }, background: { backgroundColor: palette.canvas }, grid: { size: 20, visible: true, type: 'dot', config: { color: palette.grid, thickness: 1 } }, polyline: { stroke: palette.muted, strokeWidth: 1.4 }, anchor: { r: 4, fill: palette.surface, stroke: palette.accent, hover: { r: 6, fill: palette.accentSoft } }, edgeText: { fontSize: 10, fill: palette.muted, background: { fill: palette.canvas, stroke: 'transparent' } } });
  }
  function interaction() {
    const silent = readOnly || disabled;
    lf.updateEditConfig({ isSilentMode: silent, nodeTextEdit: false, edgeTextEdit: false, stopMoveGraph: false, stopZoomGraph: false, stopScrollGraph: true });
    if (silent) lf.keyboard.disable(); else { lf.keyboard.enable(); lf.keyboard.off(['backspace']); }
  }
  recolor(palette); interaction();
  // Deletion always goes through the branch action; native backspace would leave
  // descendants disconnected and can also collide with parameter text editing.
  lf.keyboard.off(['backspace']);
  windowed=createWindowedGraph(lf,payloads,container,callbacks);
  // The accessible canvas host can own focus while LogicFlow's keyboard target
  // is its inner child. Capture on the host so either focus path uses the same
  // authored history, without intercepting undo inside parameter inputs.
  const historyKeys=['cmd + z','ctrl + z','cmd + y','ctrl + y','cmd + shift + z','ctrl + shift + z'];
  lf.keyboard.off(historyKeys);
  function historyShortcut(event) {
    if(readOnly||disabled||event.altKey||!(event.ctrlKey||event.metaKey)||event.target?.closest?.('input,textarea,[contenteditable=true]'))return;
    const key=event.key.toLowerCase(),direction=key==='y'||(key==='z'&&event.shiftKey)?'redo':key==='z'?'undo':null;
    if(!direction)return;
    event.preventDefault();event.stopPropagation();
    if(windowed.active)windowed[direction]();else{lf[direction]();changed();callbacks.history(lf.history.undoAble(),lf.history.redoAble());}
  }
  container.addEventListener('keydown',historyShortcut,true);
 lf.on('node:click', ({ data }) => {if(windowed.active)windowed.select(data.id);callbacks.select(data.id);});
  lf.on('edge:click', ({ data }) => callbacks.select(data.targetNodeId, data.id));
  lf.on('blank:click', () => callbacks.select(null));
  lf.on('connection:not-allowed', () => callbacks.error(new Error('连接不符合父子树要求：每个组件仅有一个父级，且不能形成循环或放入错误作用域')));
  lf.on('edge:add', ({ data }) => {
 if(windowed.rendering)return;
 const edges = windowed.active?windowed.graph.edges:lf.getGraphRawData().edges;
    const siblings = edges.filter(edge => edge.id !== data.id && edge.sourceNodeId === data.sourceNodeId);
    let order = data.properties?.order;
    if (!Number.isInteger(order) || siblings.some(edge => edge.properties?.order === order)) order = siblings.length ? Math.max(...siblings.map(edge => Number(edge.properties?.order) || 0)) + 1 : 0;
    lf.setProperties(data.id, { order }); lf.updateText(data.id, String(order + 1));
    changed();
  });
  lf.on('node:drop,node:delete,edge:delete,edge:exchange-node', changed);
  lf.on('history:change', ({ data }) => {if(windowed.active)return; callbacks.history(data.undoAble, data.redoAble); changed(); });
  lf.on('graph:transform', ({ transform }) => {callbacks.zoom?.(transform.SCALE_X);windowed.schedule();});
  function zoomAtCenter(scale) {
    const { width, height } = container.getBoundingClientRect();
    if (width <= 0 || height <= 0) return false;
    const center = lf.graphModel.transformModel.HtmlPointToCanvasPoint([width / 2, height / 2]);
    lf.zoom(Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, scale)), center);
    return true;
  }
  function fit() {
    const { width, height } = container.getBoundingClientRect();
    if (width <= 0 || height <= 0) return false;
 const bounds=windowed.bounds()||(lf.graphModel.nodes.length?lf.graphModel.getVirtualRectSize():null);if(!bounds)return false;
    // Sparse/new flows keep their natural size; only larger graphs are reduced.
    const scale = Math.max(MIN_ZOOM, Math.min(1, (width - 2 * FIT_PADDING) / bounds.width, (height - 2 * FIT_PADDING) / bounds.height));
    lf.resize(width, height);
    viewportSize = { width, height };
    lf.zoom(scale);
    lf.graphModel.transformModel.focusOn(bounds.x, bounds.y, width, height);
    return true;
  }
  return {
    instance: lf,
    load(graph) { lf.history.destroy();payloads.clear(); if(!windowed.load(graph))lf.render(payloads.render(graph)); callbacks.history(false, false); },
 getGraph(){return windowed.active?windowed.graph:authored();},
    hydrate(graph) { payloads.hydrate(graph); windowed.hydrate(graph); },
    apply(graph) {
 if(windowed.apply(graph))return;
 if(graph.nodes.length>400){const previous=authored(),past=lf.history.undos.map(state=>payloads.restore(state));if(!past.length||graphStamp(past.at(-1))!==graphStamp(previous))past.push(previous);windowed.load(graph,past);callbacks.change(graph,{authored:true});return;}
      lf.graphModel.graphDataToModel(payloads.render(graph));
      lf.history.add(lf.getGraphRawData());
      changed();
    },
    select(id) {if(windowed.select(id))return; lf.clearSelectElements(); if (id) lf.selectElementById(id); },
    undo() {if(windowed.active){windowed.undo();return;} lf.undo(); changed(); callbacks.history(lf.history.undoAble(), lf.history.redoAble()); },
    redo() {if(windowed.active){windowed.redo();return;} lf.redo(); changed(); callbacks.history(lf.history.undoAble(), lf.history.redoAble()); },
    fit,
    zoom(increase) { zoomAtCenter(Math.round((lf.getTransform().SCALE_X + (increase ? ZOOM_STEP : -ZOOM_STEP)) * 1000) / 1000); },
    resetZoom() { zoomAtCenter(1); },
    resize() {
      const { width, height } = container.getBoundingClientRect();
      if (width <= 0 || height <= 0) return;
      lf.resize(width, height);
      if (viewportSize.width > 0 && viewportSize.height > 0) lf.translate((width - viewportSize.width) / 2, (height - viewportSize.height) / 2);
      viewportSize = { width, height };
    }, destroy() {container.removeEventListener('keydown',historyShortcut,true);windowed.destroy(); lf.destroy(); payloads.clear(); },
    setDisabled(value) { disabled = value; interaction(); },
    setReadOnly(value) { readOnly = value; interaction(); },
    setTheme: recolor,
  };
}
