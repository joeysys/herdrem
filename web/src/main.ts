import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './styles/base.css'
import './styles/themes.css'
import { useTheme } from './composables/useTheme'
import { useBackground } from './composables/useBackground'

useTheme().init()
useBackground().init()

const app = createApp(App)
app.use(createPinia())
app.mount('#app')
