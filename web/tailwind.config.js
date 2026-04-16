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
          base: '#f8fafc',
          card: '#ffffff',
          hover: '#f1f5f9',
        },
        fg: {
          primary: '#1e293b',
          muted: '#64748b',
        },
        accent: '#2563eb',
        danger: '#dc2626',
        border: '#e2e8f0',
      },
    },
  },
  plugins: [],
}
