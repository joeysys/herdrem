/** Build ID virtual module — git short hash + build counter.
 *  Re-evaluated on each Vite rebuild (including watch mode). */
declare module 'virtual:build-id' {
  export const buildId: string
}
