<script setup>
defineProps({ name: String, size: { type: Number, default: 20 } });
</script>
<template>
  <svg :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <template v-if="name === 'tasks'"><rect x="5" y="5" width="14" height="17" rx="2"/><path d="M9 5V3h6v2M9 11h6M9 15h4"/></template>
    <path v-else-if="name === 'code'" d="m8 7-5 5 5 5m8-10 5 5-5 5m-3-13-2 22"/>
    <template v-else-if="name === 'book'"><path d="M12 5C9 2 5 3 3 4v16c3-2 6-2 9 0 3-2 6-2 9 0V4c-2-1-6-2-9 1Zm0 0v15"/><path d="M6 8h3M6 12h3M15 8h3M15 12h3"/></template>
    <path v-else-if="name === 'folder'" d="M3 7V5h7l2 3h9v12H3V7Z"/>
    <template v-else-if="name === 'flow'"><rect x="9" y="2" width="6" height="5" rx="1"/><rect x="2" y="17" width="6" height="5" rx="1"/><rect x="16" y="17" width="6" height="5" rx="1"/><path d="M12 7v5M5 17v-5h14v5"/></template>
    <template v-else-if="name === 'cluster'"><rect x="3" y="4" width="18" height="6" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7h.01M7 17h.01M17 7h1M17 17h1"/></template>
    <path v-else-if="name === 'refresh'" d="M20 7v5h-5M20 12a8 8 0 1 0-2 6"/>
    <path v-else-if="name === 'plus'" d="M12 5v14M5 12h14"/>
    <template v-else-if="name === 'search'"><circle cx="10" cy="10" r="6"/><path d="m15 15 5 5"/></template>
    <template v-else-if="name === 'user'"><circle cx="12" cy="7" r="4"/><path d="M4 22v-3a8 8 0 0 1 16 0v3Z"/></template>
    <template v-else-if="name === 'pulse'"><rect x="2" y="2" width="20" height="20" rx="3"/><path d="M2 12h5l3-5 4 11 3-6h5"/></template>
    <path v-else-if="name === 'close'" d="m6 6 12 12M6 18 18 6"/>
    <path v-else-if="name === 'down'" d="m6 9 6 6 6-6"/>
    <path v-else-if="name === 'left'" d="m15 6-6 6 6 6"/>
    <path v-else-if="name === 'right'" d="m9 6 6 6-6 6"/>
    <path v-else-if="name === 'upload'" d="M12 16V3m-5 5 5-5 5 5M4 15v6h16v-6"/>
    <path v-else-if="name === 'file'" d="M14 2H5v20h14V7l-5-5Zm0 0v6h5M8 13h8M8 17h6"/>
    <path v-else-if="name === 'check'" d="m5 12 4 4L19 6"/>
    <template v-else-if="name === 'sun'"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5"/></template>
    <path v-else-if="name === 'moon'" d="M20 14a8 8 0 0 1-10-10 8.5 8.5 0 1 0 10 10Z"/>
    <template v-else-if="name === 'device'"><rect x="3" y="3" width="18" height="13" rx="2"/><path d="M8 21h8M12 16v5"/></template>
    <template v-else-if="name === 'palette'"><path d="M12 3a9 9 0 1 0 0 18h1a2 2 0 0 0 1-3.7 1.7 1.7 0 0 1 .9-3.3H18a3 3 0 0 0 3-3c0-4.4-4-8-9-8Z"/><path d="M7 10h.01M10 7h.01M15 7h.01M17 10h.01"/></template>
    <path v-else-if="name === 'download'" d="M12 3v13m-5-5 5 5 5-5M4 16v5h16v-5"/>
    <template v-else-if="name === 'save'"><path d="M20 21H4V3h12l4 4v14Z"/><path d="M8 3v6h8V3M8 21v-8h8v8"/></template>
    <template v-else-if="name === 'eye'"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></template>
    <path v-else-if="name === 'expand'" d="M3 9V3h6M15 3h6v6M21 15v6h-6M9 21H3v-6"/>
    <template v-else-if="name === 'settings'"><path d="m9 3-.7 2.2-2 .9L4 5.6 2.5 8.3l1.6 1.8-.2 2.3-1.4 1.8L4 17l2.3-.3 2 1L9 20h3l.8-2.2 2-1 2.2.3 1.6-2.8-1.4-1.9-.2-2.2 1.6-1.9L17 5.6l-2.3.4-2-.9L12 3H9Z"/><circle cx="10.5" cy="11.5" r="3"/></template>
    <template v-else-if="name === 'history'"><path d="M3 4v5h5M3 9a9 9 0 1 1 0 6"/><path d="M12 7v5l3 2"/></template>
    <template v-else-if="name === 'more'"><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></template>
    <template v-else><circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/></template>
  </svg>
</template>
