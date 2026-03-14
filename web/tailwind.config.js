/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', '"Noto Sans"', 'Helvetica', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', '"SF Mono"', 'Menlo', 'Consolas', '"Liberation Mono"', 'monospace'],
      },
      colors: {
        bg: {
          base: '#0d1117',
          card: '#161b22',
          hover: '#21262d',
        },
        fg: {
          primary: '#e6edf3',
          muted: '#7d8590',
        },
        accent: '#58a6ff',
        danger: '#f85149',
        border: '#30363d',
      },
    },
  },
  plugins: [],
}
