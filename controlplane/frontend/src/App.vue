<script setup>
import { onBeforeUnmount, ref } from 'vue';
import { api, setCsrfToken } from './api.js';
import { Icon, Loading } from './ui/index.js';
import Login from './Login.vue';
import Workspace from './Workspace.vue';
const session = ref(null), checking = ref(true), initialError = ref(null);
const controller = new AbortController();
let alive = true;
api('/session', { signal: controller.signal })
  .then(value => { if (alive) { setCsrfToken(value.csrf_token); session.value = value.authenticated ? value : null; } })
  .catch(error => { if (alive && error.status !== 401 && error.name !== 'AbortError') initialError.value = error; })
  .finally(() => { if (alive) checking.value = false; });
onBeforeUnmount(() => { alive = false; controller.abort(); });
function logout() { session.value = null; initialError.value = null; }
</script>
<template>
  <div v-if="checking" class="boot"><Icon name="pulse" :size="36"/><Loading/></div>
  <Login v-else-if="!session" :initial-error="initialError" @login="session = $event"/>
  <Workspace v-else @logout="logout"/>
</template>
