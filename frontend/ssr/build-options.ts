/**
 * esbuild options factory for the SSR bundle (Vue 3 + QuickJS / Goja / V8go).
 *
 * Pure: returns an options object only — caller invokes esbuild.build().
 * Does not generate module registries; run `pnpm generate` separately.
 */
import path from 'node:path'
import type { BuildOptions } from 'esbuild'
import { quickjsPolyfills } from './polyfills'

export interface SSRBuildUserOptions {
  /** Entry point file (default: './ssr-esm-render.ts') */
  entryPoints?: string[]
  /** Output file path (default: 'dist/ssr-render-cjs.js') */
  outfile?: string
  /** esbuild plugins (vuePlugin is required) */
  plugins: NonNullable<BuildOptions['plugins']>
  /** Path aliases (merged with defaults) */
  alias?: Record<string, string>
  /** Additional esbuild define values (merged with Vue defaults) */
  define?: Record<string, string>
  /** Enable full minification + tree-shaking. Default: true. */
  minify?: boolean
  /** Any additional esbuild options to merge (overrides all defaults) */
  esbuildOptions?: Partial<BuildOptions>
}

const defaultAlias: Record<string, string> = {
  '@': './src',
  '~': './src',
  '@pages': './pages',
  '~pages': './pages',
}

const vueDefines: Record<string, string> = {
  __VUE_OPTIONS_API__: 'false',
  __VUE_PROD_DEVTOOLS__: 'false',
  __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false',
  __VUE_FEATURE_SUSPENSE__: 'false',
  __VUE_FEATURE_TELEPORT__: 'false',
  __VUE_FEATURE_TRANSITION__: 'false',
  __VUE_FEATURE_KEEP_ALIVE__: 'false',
  __VUE_FEATURE_SCOPED_SLOT__: 'false',
  'process.env.NODE_ENV': '"production"',
  'process.env.VUE_ENV': '"server"',
}

export function createSSRBuildOptions(options: SSRBuildUserOptions): BuildOptions {
  const {
    entryPoints = ['./ssr-esm-render.ts'],
    outfile = 'dist/ssr-render-cjs.js',
    plugins,
    alias = {},
    define = {},
    minify = true,
    esbuildOptions = {},
  } = options

  return {
    entryPoints,
    bundle: true,
    platform: 'node',
    format: 'cjs',
    outfile,
    plugins,

    banner: { js: quickjsPolyfills },

    resolveExtensions: ['.vue', '.js', '.ts'],
    alias: { ...defaultAlias, ...alias },
    nodePaths: [path.resolve(process.cwd(), 'node_modules')],

    minify,
    treeShaking: minify,

    define: { ...vueDefines, ...define },

    target: ['es2020', 'node16'],
    sourcemap: false,
    metafile: false,
    write: true,
    dropLabels: ['DEV', 'DEBUG', 'TEST'],
    keepNames: false,
    legalComments: 'none',
    charset: 'utf8',
    conditions: ['node', 'import'],

    ...esbuildOptions,
  }
}
