import { defineAsyncComponent, h } from 'vue';

export default defineAsyncComponent({
  loader: () => import('./CodePreview.vue').then(module => module.default), delay: 100, timeout: 30000,
  loadingComponent: () => h('p', { class: 'notice', role: 'status' }, '正在加载源码预览…'),
  errorComponent: () => h('p', { class: 'notice danger', role: 'alert' }, '源码预览加载失败，原始内容已保留。可重新打开预览重试。'),
});
