import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // Load the .env file from the project root instead of central-ui/.
  envDir: '..',
})
