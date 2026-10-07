/** Build ID virtual module.
 *  Clean tree: git short HEAD. Dirty tree: that plus a worktree fingerprint.
 *  No git: nogit+source fingerprint. Dev/watch also appends a rebuild counter. */
declare module 'virtual:build-id' {
  export const buildId: string
}
