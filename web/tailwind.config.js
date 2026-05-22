/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Palette ported verbatim from the previous --css-vars in
        // web/index.html so the dark-theme look stays identical.
        bg: '#0d1117',
        panel: '#161b22',
        'panel-2': '#1c2128',
        border: '#30363d',
        fg: '#e6edf3',
        muted: '#8b949e',
        accent: '#58a6ff',
        green: '#3fb950',
        yellow: '#d29922',
        red: '#f85149',
        purple: '#bc8cff',
      },
      fontFamily: {
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"Segoe UI"',
          'Roboto',
          '"Helvetica Neue"',
          'sans-serif',
        ],
        mono: ['ui-monospace', '"SF Mono"', 'Menlo', 'Consolas', 'monospace'],
      },
      animation: {
        'pulse-dot': 'pulseDot 1.5s ease-in-out infinite',
      },
      keyframes: {
        pulseDot: {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.4' },
        },
      },
    },
  },
  plugins: [],
};
