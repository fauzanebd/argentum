// @ts-check
import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

// The dashboard has never been linted: `pnpm lint` called eslint, eslint was
// never in devDependencies, and the script failed with "command not found" in
// CI and locally alike (finding Q-11). This config is the narrow restart —
// recommended sets only, no stylistic rules, and the two React plugins that
// catch real bugs rather than preferences. Widen it once the tree is clean
// and stays clean.
export default tseslint.config(
  {
    // Build output and generated files. tokens.generated.css has no JS in it,
    // but dist/ does and linting a bundle produces thousands of findings.
    ignores: ['dist', 'node_modules', '.wrangler'],
  },
  js.configs.recommended,

  // Type-unaware rules only. The type-checked preset needs a program per lint
  // run, which roughly triples the time and duplicates what `tsc -b --noEmit`
  // already tells us in the same script.
  ...tseslint.configs.recommended,

  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,

      // A component file that also exports a helper breaks Vite's fast
      // refresh: the whole module reloads instead of the component. A warning,
      // because the fix is sometimes a file split.
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],

      // `_`-prefixed parameters are the codebase's existing way of saying
      // "required by the signature, unused here".
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
    },
  },

  {
    // Node context, not browser: vite/tailwind/postcss config files.
    files: ['*.config.{js,ts}', 'vite.config.ts', 'tailwind.config.js', 'postcss.config.js'],
    languageOptions: { globals: globals.node },
  },
)
