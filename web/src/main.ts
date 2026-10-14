import { createApp } from 'vue'
import App from './App.vue'
import './styles/viz.css'
import './styles/cards.css'
import { router } from './router'
import { i18n } from './i18n'

createApp(App).use(router).use(i18n).mount('#app')
