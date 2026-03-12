/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        display: ['Bebas Neue', 'sans-serif'],
        body: ['Lora', 'serif'],
        mono: ['DM Mono', 'monospace'],
      },
      colors: {
        bg: {
          base: '#0f0f0f',
          card: '#1a1a1a',
          hover: '#242424',
        },
        fg: {
          primary: '#e8e4dc',
          muted: '#7a7570',
        },
        accent: '#c8f03c',
        danger: '#e05a4e',
        border: '#2e2e2e',
      },
    },
  },
  plugins: [],
}
