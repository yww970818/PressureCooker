import { createApp } from 'vue';
import App from './App.vue';
import './styles.css';
import './ui/dropdowns.css';
import { initTheme } from './theme.js';
initTheme();
createApp(App).mount('#root');
