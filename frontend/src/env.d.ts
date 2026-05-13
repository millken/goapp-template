// Allow CSS side-effect imports in TypeScript
declare module '*.css' {
  const css: string
  export default css
}
