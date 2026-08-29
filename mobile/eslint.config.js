// @ts-check
const expo = require('eslint-config-expo/flat');

/**
 * Lint, and the one rule that is not about style.
 *
 * `no-restricted-syntax` below fails the build on a colour literal written
 * outside src/theme/. It is the mechanical half of the design system: tokens
 * that are merely documented drift, because the day somebody needs "just a
 * slightly different green" nothing stops them and nothing says so afterwards.
 */
module.exports = [
  ...expo,
  {
    ignores: ['node_modules/**', 'dist/**', '.expo/**', 'web-build/**', 'coverage/**'],
  },
  {
    files: ['jest.setup.js', 'jest.config.js', 'babel.config.js', 'eslint.config.js'],
    languageOptions: {
      globals: {
        jest: 'readonly',
        module: 'writable',
        require: 'readonly',
        __dirname: 'readonly',
      },
    },
  },
  {
    files: ['**/*.{ts,tsx,js,jsx,mjs}'],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          // #fff, #ffffff, #ffffffff — any hex colour, anywhere in a string.
          selector:
            "Literal[value=/#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})\\b/]",
          message:
            'Colour literals belong in src/theme/. Import a token instead — ' +
            'see src/theme/colors.ts, and phase-09 §E2.',
        },
        {
          // rgb()/rgba()/hsl() are the same rule wearing a different hat.
          selector: "Literal[value=/\\b(?:rgba?|hsla?)\\s*\\(/]",
          message:
            'Colour literals belong in src/theme/. Import a token instead — ' +
            'see src/theme/colors.ts, and phase-09 §E2.',
        },
      ],
    },
  },
  {
    // The tokens themselves, and the tests that check them, are where the
    // literals live.
    files: ['src/theme/**/*.ts', 'src/theme/**/*.tsx'],
    rules: { 'no-restricted-syntax': 'off' },
  },
  {
    // Build tooling runs in Node, not in the app.
    //
    // The colour rule above covers it too. These scripts sit outside src/ and
    // used to carry their own copies of the palette, which is the drift the
    // rule exists to prevent — and in tools/audit.mjs it was worse than
    // cosmetic: a stale copy of the gold tokens would have made the gold-area
    // cap pass on everything. They read tools/theme.mjs instead.
    files: ['tools/**/*.mjs'],
    languageOptions: {
      globals: { Buffer: 'readonly', process: 'readonly', console: 'readonly' },
    },
  },
  {
    // The service worker runs in neither: no window, no document, and `self`
    // is a ServiceWorkerGlobalScope. Linting it against the browser's globals
    // reports `caches` and `clients` as undefined, which they are not there.
    files: ['public/sw.js'],
    languageOptions: {
      globals: {
        self: 'readonly',
        caches: 'readonly',
        clients: 'readonly',
        fetch: 'readonly',
        URL: 'readonly',
      },
    },
  },
];
